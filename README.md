# ft
ft is a file browser that allows for file operations remotely. ft has a server/client model and this is the server.

## usage
Build it through `go build` then invoke the command:

``` shell
# replace $DIR with your directory
ft "$DIR"
```

By default, `ft` runs at :8080, however this behavior can be changed through setting the environment variable `FT_ADDR`. The `FT_ADDR` environment variable uses Go's internal `net` package and so must adhere to its syntax, for more information on the address syntax [check out this page](https://godocs.io/net#Listen).

## api
While there is no API documentation currently, it is very easy to use the API as it is pretty simple. Here's a few pointers:
1. any client that connects to the server side events route `/sse` is a user
2. all users must supply an `Authentication: Bearer $ID` cookie, with $ID being replace by the first event that is sent when the user connects to `/sse`
3. all routes have clearly defined data structures in the internal controller package, use `go doc` to see the internal data structures
4. all data passed and returned are encoded in JSON
