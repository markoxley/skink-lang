# Skink Web Framework

The `std/web` package provides a minimal HTTP router, request/response types, and a server helper for building RESTful APIs.

## Modules

- `std/web/types` — `Request`, `ResponseWriter`, and `Handler` types.
- `std/web/router` — `Router` with route registration and dispatch.
- `std/web/server` — HTTP request parsing and response serialization.

## Request

`Request` is passed by value to handlers. It exposes the following fields and methods:

| Field / Method | Description |
| --- | --- |
| `method: string` | HTTP method (`GET`, `POST`, etc.). |
| `path: string` | Request path. |
| `query: string` | Raw query string (e.g. `q=skink&limit=10`). |
| `headers: map[string]string` | Request headers. |
| `body: string` | Request body as a string. |
| `Method()` | Returns `method`. |
| `Path()` | Returns `path`. |
| `Query()` | Returns the raw query string. |
| `Header(key)` | Returns the request header for `key`. |
| `Body()` | Returns the request body. |
| `URLParam(name)` | Returns a path parameter captured by `/:name` routes. |
| `QueryParam(name)` | Returns a single query parameter by name. |
| `ParseJSON()` | Parses the request body as a `json.Value`. |

### Path parameters

Routes defined with `/:param` segments populate path parameters into the request headers map under the key `__param_<name>`. The `URLParam` method reads this value.

```skink
r.Get("/user/:id", fn(req: types.Request, w: *types.ResponseWriter) {
    id := req.URLParam("id")
    w.Write("User ID: ")
    w.Write(id)
})
```

### Query parameters

```skink
r.Get("/search", fn(req: types.Request, w: *types.ResponseWriter) {
    q := req.QueryParam("q")
    w.Write("Search: ")
    w.Write(q)
})
```

### JSON request body

```skink
r.Post("/echo", fn(req: types.Request, w: *types.ResponseWriter) {
    body, _ := req.ParseJSON()
    w.JSONValue(200, body)
})
```

## ResponseWriter

`ResponseWriter` is a pointer receiver type used by handlers to build the response.

| Method | Description |
| --- | --- |
| `WriteHeader(status)` | Sets the HTTP status code. |
| `Write(data)` | Appends data to the response body. |
| `Header(key, value)` | Sets a response header. |
| `JSON(status, data)` | Sets the status, body, and `Content-Type: application/json`. |
| `JSONValue(status, v)` | Serializes `json.Value` `v` and sets `Content-Type: application/json`. |
| `HTML(status, data)` | Sets the status, body, and `Content-Type: text/html; charset=utf-8`. |
| `Status()` | Returns the current status code. |
| `ResponseBody()` | Returns the current response body. |

## Router

Create a router with `router.NewRouter()` and register routes using the HTTP method helpers.

```skink
import "std/web/router"
import "std/web/types"

fn main() -> int {
    r := router.NewRouter()

    r.Get("/", fn(req: types.Request, w: *types.ResponseWriter) {
        w.Write("hello")
    })

    r.Get("/user/:id", fn(req: types.Request, w: *types.ResponseWriter) {
        w.Write("User ID: ")
        w.Write(req.URLParam("id"))
    })

    r.Get("/search", fn(req: types.Request, w: *types.ResponseWriter) {
        w.Write("q=")
        w.Write(req.QueryParam("q"))
    })

    r.Post("/echo", fn(req: types.Request, w: *types.ResponseWriter) {
        body, _ := req.ParseJSON()
        w.JSONValue(200, body)
    })

    // Not found handler
    r.Get("/missing", fn(req: types.Request, w: *types.ResponseWriter) {
        w.WriteHeader(404)
        w.Write("Not Found")
    })

    return 0
}
```

## Handler signature

The `Handler` type is defined as:

```skink
pub type Handler = fn(Request, *ResponseWriter)
```

Handlers take a `Request` by value and a `ResponseWriter` pointer.

## Server helper

`std/web/server` provides `ReadRequest(conn)` to parse a raw HTTP request string into a `Request`, and `serializeResponse(w)` to build an HTTP response string from a `ResponseWriter`. The server module is used by the router's test helpers and can be used directly when building a network server.

## Status codes

The server helper supports the following common status codes out of the box:

- `100 Continue`
- `200 OK`
- `201 Created`
- `204 No Content`
- `301 Moved Permanently`
- `302 Found`
- `304 Not Modified`
- `400 Bad Request`
- `401 Unauthorized`
- `403 Forbidden`
- `404 Not Found`
- `405 Method Not Allowed`
- `409 Conflict`
- `422 Unprocessable Entity`
- `500 Internal Server Error`
- `502 Bad Gateway`
- `503 Service Unavailable`

## Full example

See `compiler/examples/web_router.skink` for a runnable example covering dynamic routes, query parameters, JSON request/response echo, and a 404 fallback.
