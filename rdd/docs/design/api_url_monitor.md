# URL Monitor API

The `URLMonitor` API periodically downloads a URL, records when the content has last changed, and caches the content.

It will be used to watch the `k3s-version.json` file and the upgrade responder information.

## Example

```yaml
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: URLMonitor
metadata:
  name: upgrade-responder
  namespace: rancher-desktop
spec:
  url: https://desktop.version.rancher.io/v1/checkupgrade
  method: POST
  body: '{"appVersion":"1.16.0"}'
  nextCheck: "2025-06-09T05:23:56Z"
  interval: 1h
status:
  lastCheck: "2025-06-09T04:55:00Z"
  lastStatus: 200
```

## Questions

* Should this use the resource API to download and cache the contents?

* Use Modified-Since and ETag headers for optimization
