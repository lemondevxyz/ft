package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/lemondevxyz/ft/internal/controller"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
)

type request struct {
	c *gin.Context
}

func (r *request) Error(err model.ControllerError) {
	r.c.JSON(400, err)
}

func (r *request) Value(val interface{}) error {
	r.c.Status(200)
	enc := json.NewEncoder(r.c.Writer)
	err := enc.Encode(val)
	if err != nil {
		return err
	}

	r.c.Writer.Flush()
	return nil
}

type server struct {
	dev          bool
	addr         string
	r            *http.Server
	channel      *controller.Channel
	fs           afero.Fs
	opController *controller.OperationController
	opFs         *controller.FsController
}

func (s *server) Start() error {
	if s.r != nil {
		return fmt.Errorf("server has already been started")
	}

	router := gin.Default()

	apikey := os.Getenv("API_KEY")

	if s.dev {
		corsHandler := cors.New(cors.Config{
			AllowAllOrigins:  true,
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "HEAD"},
			AllowHeaders:     []string{"If-None-Match", "Origin", "Content-Length", "Content-Type", "Authorization", "Accept", "ETag"},
			AllowCredentials: false,
			MaxAge:           12 * time.Hour,
			ExposeHeaders:    []string{"ETag"},
		})

		router.Use(corsHandler)
	}

	router.GET("/client/*filepath", func(c *gin.Context) {
		c.File("./client/index.html")
	})
	router.Static("/static", "./static/")
	router.StaticFS("/files", afero.NewHttpFs(s.fs))
	router.GET("/api/v0/sse", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Connection", "keep-alive")
		c.Header("Cache-Control", "no-cache")

		c.Writer.Flush()

		first := false

		var id string
		var ch chan sse.Event

		c.Stream(func(w io.Writer) bool {
			if !first {
				if len(apikey) > 0 {
					id = apikey
					ch = s.channel.SetSubscriber(id)
				} else {
					id, ch = s.channel.Subscribe()
				}

				sse.Encode(w, sse.Event{
					Event: "id",
					Data:  id,
				})

				first = true
				go func() {
					time.Sleep(time.Second)
					ch <- controller.EventOperationAll(s.opController.Operations())
				}()
			} else {
				select {
				case <-time.After(time.Second):
				case event := <-ch:
					sse.Encode(w, event)
				}
			}

			return true
		})

		s.channel.Unsubscribe(id)
	})

	shouldProtect := os.Getenv("FT_PROTECT_API")
	protected := router.Group("/api/v0/", func(c *gin.Context) {
		if len(shouldProtect) > 0 {
			id := strings.ReplaceAll(c.GetHeader("Authorization"), "Bearer ", "")
			if len(apikey) > 0 {
				if id != apikey {
					c.AbortWithStatusJSON(401, model.ControllerError{
						ID:     "authorization",
						Reason: "invalid id (api key)",
					})
					return
				}
			} else {
				sub := s.channel.GetSubscriber(id)
				if sub == nil {
					c.AbortWithStatusJSON(401, model.ControllerError{
						ID:     "authorization",
						Reason: "invalid id",
					})
					return
				}
			}

			c.Set("id", id)
		}

		c.Header("Content-Type", "application/json")
		c.Header("Accept", "application/json")

		if c.GetHeader("Content-Type") != "application/json" {
			c.AbortWithStatusJSON(401, model.ControllerError{
				ID:     "encoding",
				Reason: "all data must be encoded in json",
			})
			return
		}

		c.Set("req", &request{c})
	})

	{
		op := protected.Group("/op")

		op.POST("/new", func(c *gin.Context) { s.opController.NewOperation(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/status", func(c *gin.Context) { s.opController.Status(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/proceed", func(c *gin.Context) { s.opController.Proceed(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/set-sources", func(c *gin.Context) { s.opController.SetSources(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/set-index", func(c *gin.Context) { s.opController.SetIndex(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/set-rate-limit", func(c *gin.Context) { s.opController.SetRateLimit(c.Request.Body, c.MustGet("req").(model.Controller)) })

	}
	{
		fs := protected.Group("/fs")

		fs.POST("/remove", func(c *gin.Context) { s.opFs.RemoveAll(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/move", func(c *gin.Context) { s.opFs.Move(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/mkdir", func(c *gin.Context) { s.opFs.MkdirAll(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/readdir", func(c *gin.Context) {
			body := bytes.Buffer{}

			_, err := io.Copy(&body, c.Request.Body)
			if err != nil {
				c.JSON(200, model.ControllerError{
					ID:     "io-copy",
					Reason: err.Error(),
				})
				return
			}

			bodyCopy := bytes.NewBuffer(body.Bytes())

			val := &controller.ReadDirData{}
			dec := json.NewDecoder(&body)
			if err := dec.Decode(val); err != nil {
				c.JSON(400, model.ControllerError{
					ID:     "json-decoder",
					Reason: err.Error(),
				})
				return
			}

			stat, err := s.fs.Stat(val.Name)
			if err != nil {
				c.JSON(400, model.ControllerError{
					ID:     "fs-stat",
					Reason: err.Error(),
				})
				return
			}

			hsh := xxhash.New()
			hsh.Write([]byte(stat.ModTime().String()))

			sum := strconv.FormatUint(hsh.Sum64(), 16)

			if string(sum) == c.GetHeader("If-None-Match") {
				c.String(304, sum)
				return
			}

			c.Header("ETag", sum)

			s.opFs.ReadDir(bodyCopy, c.MustGet("req").(model.Controller))
		})
		fs.POST("/verify", func(c *gin.Context) { s.opFs.Verify(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/size", func(c *gin.Context) { s.opFs.Size(c.Request.Body, c.MustGet("req").(model.Controller)) })
	}

	s.r = &http.Server{
		Handler: router,
	}

	// tcp = 0
	// unix = 1
	var mode = "tcp"
	var addr = s.addr
	if strings.HasPrefix(s.addr, "unix!") {
		mode = "unix"
		addr = s.addr[5:]
	} else if strings.HasPrefix(s.addr, "tcp!") {
		addr = s.addr[4:]
	}

	ln, err := net.Listen(mode, addr)
	if err != nil {
		return err
	}

	go func() {
		s.r.Serve(ln)
	}()

	return nil
}

func (s *server) IsRunning() bool {
	return s.r != nil
}

func (s *server) Stop() error {
	return s.r.Close()
}

func NewWebInstance(addr string, fs afero.Fs, dev bool) (model.Server, error) {
	sr := &server{
		dev:          dev,
		addr:         addr,
		channel:      &controller.Channel{},
		fs:           fs,
		opController: &controller.OperationController{},
		opFs:         &controller.FsController{},
	}

	var err error

	sr.opController, err = controller.NewOperationController(sr.channel, sr.fs)
	if err != nil {
		return nil, err
	}

	sr.opFs, err = controller.NewFsController(sr.channel, sr.fs)
	if err != nil {
		return nil, err
	}

	return sr, nil
}
