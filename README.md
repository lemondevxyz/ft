# ft
ft is a file browser that allows for file operations remotely. ft has a server/client model and this is the server.

You could try to use `ft` through [ft.lemondev.xyz](https://ft.lemondev.xyz).

[localhost_3000_tmp_container.webm](https://user-images.githubusercontent.com/62130041/196074018-9a9edd77-88eb-4b7a-a409-d847911fd8ef.webm)

## features
- [x] Creating Directories
- [x] Copying files & directories
- [x] Removing files & directories
- [x] Moving or renaming files and directories
- [x] Notification of real-time updates that happen to the filesystem
- [x] Verifying files
- [x] Pausing, Resuming and cancelling opeartions
- [x] Adding more files to the operations or removing operations
- [x] Manual transfer speed limit
- [x] OpenAPI specification

## non-features
- [ ] Authentication; should be done by an outside application
- [ ] Mounting or unmounting filesystems; if wanted, should be done by an outside application
- [ ] Any execution mechanism for file
- [ ] File Transfer History; if wanted, should be implemented by the client but not the server
- [ ] Exposing the file system to other protocols; use sshd, 9p or webdav-daemon

## usage
Build it through `go build` then invoke the command:

``` shell
# set the FT_ADDR environment variable
FT_ADDR="localhost:8080"
# replace $DIR with the directory you want
# to expose 
ft "$DIR"
# do note: any directory inside $DIR that's linked
# to an outside directory won't work
```

By default, `ft` is exposed to every ip address and at port `8080`. However, this behaivor is customizable through setting the variable `FT_ADDR`.

The `FT_ADDR` can be made to listen to either TCP or Unix sockets. Checkout the following example:
```
# listens on unix socket /tmp/ok.sock
FT_ADDR="unix!/tmp/ok.sock"
# listens on tcp socket 127.0.0.1:12345
FT_ADDR="tcp!127.0.0.1:12345"
```

## api
The API is simple; there are two "sections" of the API. The operation section of the API, mounted at "/api/v0/op". And the file system section of the API, mounted at "/api/v0/fs".

> Operation refers to a copying operation, like copying a directory or a handful of files to another directory.

Operations contain an array of paths that could point to either a file or a directory.

The operation section has these routes:
- `POST /api/v0/op/new`: Creates a new operation
- `POST /api/v0/op/status`: Changes the status of the operation (For example, pauses the operation)
- `POST /api/v0/op/proceed`: A special router that's called after an error occurs
- `POST /api/v0/op/set-sources`: Sets the sources(files and directories to copy) of the operation
- `POST /api/v0/op/set-index`: Sets the current file to copy by its index in the sources array; can be used to skip a file or two

> In `ft`'s api, you are not supposed to **request data about operations**. You receive it from the `/sse` route and save it in the application's memory. This is to ensure multiple clients can use `ft` without any syncing issues.

The filesystem section has these routes:
- `POST /api/v0/fs/remove`: Removes a file or directory and **all of its files**
- `POST /api/v0/fs/move`: Moves a file or directory
- `POST /api/v0/fs/mkdir`: Creates a directory
- `POST /api/v0/fs/readdir`: Reads a directory and returns its files
- `POST /api/v0/fs/verify`: Verifies if two files contents are the same
- `POST /api/v0/fs/size`: Calculates the real size of a directory

> `/api/v0/fs/readdir` can be cached through the ETag Header that it sends. In-fact, in `ft-client` ReadDir calls are consistenly re-fetched but never re-written.

Lastly, there are 4 other important routes. One being the `/api/v0/sse`, which sends server-side events (SSE) to the client. You could view the SSE events and their data structure through the event file located at [internal/controller/event.go](internal/conntroller/event.go).

`/files` which returns opens and reads any files for execution. The browser determines whether to execute or to download the file.

`/static` which serves all files in `$PWD/static` directory.

`/client` which serves `$PWD/client/index.html` for the parent directory and all subsequent files. (For example, `/client/a/b/c` will serve `$PWD/client/index.html`).

> If you are still lost, load the oas.json OpenAPI specification and hopefully you could catch up.
