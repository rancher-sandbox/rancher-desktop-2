# Embedded Resources

Controllers can embed binary resources (disk images, data files) in the `rdd` binary using Go's `embed` package. Other controllers access these resources through an `embed://` URL scheme, regardless of whether they run in the same process or on a different machine.

The goal is to ship `rdd` as a single binary with all resources embedded. Go's `embed.FS` memory-maps the data, so embedded files do not consume heap memory.

## URL Format

```
embed://<controller>/<path>
```

The `<controller>` is the controller name (e.g., `demo`, `limavm`). The `<path>` identifies the file within the provider's filesystem.

Example: `embed://demo/opensuse.iso`

## Provider Side

A controller embeds resources by calling `embed.Register()` once during `init()`:

```go
//go:embed data
var dataFS embed.FS

func init() {
    embed.Register("demo", dataFS)
    base.RegisterController(newController())
}
```

The `SharedControllerManager` discovers registered controllers automatically and creates passthrough HTTP handlers for each one. The controller does not need to implement `PassthroughController`.

The `embed.Register()` function accepts any `fs.FS` implementation. The initial use case is `embed.FS`, but controllers could use `os.DirFS` or any other implementation if needed.

## Consumer Side

A controller fetches embedded resources through `embed.Open()`:

```go
reader, err := embed.Open(ctx, "embed://demo/opensuse.iso")
```

`embed.Open()` resolves the URL in two steps:

1. **In-process lookup.** If the controller has a registered `fs.FS` in this process, open the file directly. No HTTP request, no serialization.
2. **API server fallback.** Fetch the file through the API server's passthrough proxy at `/passthrough/<controller>/embed/<path>`.

The in-process path handles the common case where provider and consumer run in the same `SharedControllerManager`. The API server path handles cross-process and cross-machine scenarios.

## Infrastructure Changes

### New package: `pkg/embed/`

| Function | Purpose |
|----------|---------|
| `Register(controller string, fs fs.FS)` | Register a filesystem for a controller. Called during `init()`. |
| `Lookup(controller string) fs.FS` | Look up a registered filesystem. Used by `SharedControllerManager`. |
| `NewHandler(controller string) http.Handler` | Create an HTTP handler that serves files from the registry. |
| `Open(ctx, url string) (io.ReadCloser, error)` | Open an embedded resource by URL. |

### SharedControllerManager

In `runPassthroughServer()`, the manager iterates its registrations and checks `embed.Lookup(registration.GetName())` for each one. When a match exists, it registers a passthrough handler at `/<controller-name>/embed/`.

### Discovery

No discovery changes needed. The passthrough system already identifies endpoints by controller name, and `enabledPassthroughs` in the discovery ConfigMap already tracks which endpoints each controller exposes. The `embed.Open()` fallback path uses the controller name from the URL directly to construct the passthrough request.

### Lima `Prepare` replacement

We implement our own `Prepare` function to replace Lima's. This function resolves `embed://` URLs through `embed.Open()` and handles image downloads for other URL schemes.

## Dependency Graph

No circular dependencies:

```
pkg/embed/               → (no internal deps)
pkg/controllers/*        → pkg/embed (Register)
pkg/service/controllers/ → pkg/embed (Lookup, NewHandler)
                         → pkg/controllers/base
```

## Future Considerations

The `fs.FS` interface hides storage details from consumers. A provider could back its `fs.FS` with a compressed tarball, and the consumer would request files by path without knowing the underlying format.

HTTP content negotiation (Accept-Encoding, Content-Encoding) can handle transfer compression transparently, as browsers do today.
