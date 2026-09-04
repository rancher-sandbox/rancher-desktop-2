# Verify the vLLM pod is serving + generating on the GPU.
#
# We do NOT use `kubectl port-forward` because the RDD k3s node has no `socat`
# (port-forward fails with "socat not found"). Instead we exec into the pod and
# hit the API over localhost — the exec channel needs no socat. Python is always
# present in the vLLM image.
$ErrorActionPreference = "Stop"
$POD = "vllm-gfx1151"

Write-Host "== Waiting for pod Ready (first run also pulls/loads the model) =="
rdd kubectl wait --for=condition=Ready "pod/$POD" --timeout=1200s

Write-Host "`n== /v1/models =="
rdd kubectl exec $POD -- python -c "import urllib.request; print(urllib.request.urlopen('http://localhost:8000/v1/models').read().decode())"

Write-Host "`n== /v1/completions (inference proof) =="
rdd kubectl exec $POD -- python -c "import urllib.request,json; d=json.dumps({'model':'Qwen/Qwen2.5-0.5B-Instruct','prompt':'The capital of France is','max_tokens':16}).encode(); req=urllib.request.Request('http://localhost:8000/v1/completions',data=d,headers={'Content-Type':'application/json'}); print(urllib.request.urlopen(req).read().decode())"

Write-Host "`n== Server log tail (look for '200 OK' + 'generation throughput' + 'GPU KV cache') =="
rdd kubectl logs $POD --tail=15
