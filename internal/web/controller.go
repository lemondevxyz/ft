package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

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
			AllowHeaders:     []string{"Origin", "Content-Length", "Content-Type", "Authorization", "Accept"},
			AllowCredentials: false,
			MaxAge:           12 * time.Hour,
		})

		router.Use(corsHandler)
	}
	router.StaticFS("/web", afero.NewHttpFs(s.fs))
	router.GET("/sse", func(c *gin.Context) {
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

	protected := router.Group("/api/v0/", func(c *gin.Context) {
		fmt.Println(c.Request.Method)
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

		c.Header("Content-Type", "application/json")
		c.Header("Accept", "application/json")

		if c.GetHeader("Content-Type") != "application/json" {
			c.AbortWithStatusJSON(401, model.ControllerError{
				ID:     "encoding",
				Reason: "all data must be encoded in json",
			})
			return
		}

		c.Set("id", id)
		c.Set("req", &request{c})
	})

	{
		op := protected.Group("/op")

		op.POST("/new", func(c *gin.Context) { s.opController.NewOperation(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/pause", func(c *gin.Context) { s.opController.Pause(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/resume", func(c *gin.Context) { s.opController.Resume(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/exit", func(c *gin.Context) { s.opController.Exit(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/start", func(c *gin.Context) { s.opController.Start(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/proceed", func(c *gin.Context) { s.opController.Proceed(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/set-sources", func(c *gin.Context) { s.opController.SetSources(c.Request.Body, c.MustGet("req").(model.Controller)) })
		op.POST("/size", func(c *gin.Context) { s.opController.Size(c.Request.Body, c.MustGet("req").(model.Controller)) })
	}
	{
		fs := protected.Group("/fs")

		fs.POST("/remove", func(c *gin.Context) { s.opFs.RemoveAll(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/move", func(c *gin.Context) { s.opFs.Move(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/mkdir", func(c *gin.Context) { s.opFs.MkdirAll(c.Request.Body, c.MustGet("req").(model.Controller)) })
		fs.POST("/readdir", func(c *gin.Context) { s.opFs.ReadDir(c.Request.Body, c.MustGet("req").(model.Controller)) })
	}

	s.r = &http.Server{
		Addr:    s.addr,
		Handler: router,
	}

	go func() {
		s.r.ListenAndServe()
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
