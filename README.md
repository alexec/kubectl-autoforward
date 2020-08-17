# Automatic Port Forwarding

I get pretty sick of writing wrappers to `kubectl port-forward` to fork it and then add the correct parameters, just to find out it can't cope with scaling events or pod deletion (AFAIK).

This tool listens for pod creation or deletion and sets up forwards automatically for you.

Install:

```bash
go install github.com/alexec/kubectl-autoforward
```

Run:

```bash
kubectl autoforward
```

## FAQ

Why is it `autoforward` not `auto-forward`? Kubectl plugins must not have hyphens in their name.