# Configuration Guide

## Overview

The copilot-api service is configured entirely through **environment variables**. There is no config file loader built in yet.

## Quick Start

### 1. Local Development

```bash
# Copy the example and customize
cp config.prod.yaml.example .env

# Edit .env with your local settings
vim .env

# The service auto-loads .env at startup (existing env vars take precedence)
make run
```

### 2. Docker Compose

```yaml
services:
  copilot-api:
    image: copilot-api:latest
    env_file:
      - ./config.prod.yaml.example  # Or .env
    environment:
      # Override specific vars
      COPILOT_DATABASE_DSN: "copilot:copilot-password@tcp(mysql:3306)/copilot?parseTime=true"
```

### 3. Kubernetes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: copilot-config
data:
  COPILOT_HTTP_ADDR: "0.0.0.0:18080"
  COPILOT_LOG_LEVEL: "info"
  COPILOT_DATABASE_DRIVER: "mysql"
  # ... non-sensitive config
---
apiVersion: v1
kind: Secret
metadata:
  name: copilot-secrets
type: Opaque
stringData:
  COPILOT_JWT_HMAC_SECRET: "your-base64-secret"
  COPILOT_DATABASE_DSN: "user:pass@tcp(mysql:3306)/copilot?parseTime=true"
  COPILOT_KNOWLEDGE_EMBEDDER_API_KEY: "sk-..."
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: copilot-api
spec:
  template:
    spec:
      containers:
      - name: api
        image: copilot-api:latest
        envFrom:
        - configMapRef:
            name: copilot-config
        - secretRef:
            name: copilot-secrets
```

## Configuration Sections

See [`config.prod.yaml.example`](./config.prod.yaml.example) for detailed documentation of all variables.

| Section | Key Variables | Notes |
|---------|---------------|-------|
| **Database** | `COPILOT_DATABASE_DRIVER`, `COPILOT_DATABASE_DSN` | Use MySQL in production; add connection pool params to DSN |
| **Auth** | `COPILOT_JWT_HMAC_SECRET`, `COPILOT_AUTH_MODE` | Rotate JWT secret every 90 days |
| **Rate Limiting** | `COPILOT_RATE_LIMIT_SUBJECT`, `COPILOT_RATE_LIMIT_IP` | Format: `"requests,window"` e.g. `"100,1m"` |
| **Capabilities** | `COPILOT_CAPABILITIES_DIR` | Mount as ConfigMap or volume |
| **Observability** | `COPILOT_OTEL_EXPORTER`, `COPILOT_OTEL_OTLP_ENDPOINT` | Use OTLP for production tracing |
| **Audit** | `COPILOT_AUDIT_FALLBACK_ENABLED`, `COPILOT_AUDIT_FALLBACK_DIR` | Enable fallback with persistent storage |

## Production Checklist

Before deploying to production:

- [ ] Generate strong secrets: `openssl rand -base64 32`
- [ ] Use MySQL, not SQLite
- [ ] Add connection pool params to `COPILOT_DATABASE_DSN`
- [ ] Set `COPILOT_LOG_LEVEL=info` or `warn`
- [ ] Enable OTLP exporter with real collector endpoint
- [ ] Configure rate limits appropriate for your scale
- [ ] Mount persistent volume for `COPILOT_AUDIT_FALLBACK_DIR`
- [ ] Enable TLS at ingress/load balancer
- [ ] Set up Prometheus scraping (once `/metrics` is implemented)
- [ ] Configure HPA with min 2 replicas for HA

## Connection Pool Tuning

The DSN format supports connection pool params, but some require code changes. Current best practice:

```bash
COPILOT_DATABASE_DSN="user:pass@tcp(host:3306)/copilot?\
parseTime=true&\
maxAllowedPacket=67108864&\
timeout=10s&\
readTimeout=30s&\
writeTimeout=30s"
```

For deeper pool tuning (max open/idle conns, conn lifetime), you would need to add code after `store.OpenWithDriver()` in `cmd/copilot-api/main.go`:

```go
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(10)
db.SetConnMaxLifetime(5 * time.Minute)
```

Consider making these environment-configurable in a future PR.

## Missing Features

The following are **not yet configurable** via environment variables:

- Database connection pool size (`SetMaxOpenConns`, `SetMaxIdleConns`)
- HTTP client timeout for capability execution
- Assistant planner concurrency limit
- Scheduled task executor concurrency

These are currently hardcoded or use defaults. If you need to tune them, modify the source and rebuild.

## Secret Management

**Never commit secrets to git.** Use one of:

- **Kubernetes Secrets** (base64-encoded, consider SealedSecrets or external-secrets-operator)
- **HashiCorp Vault** with agent sidecar
- **Cloud provider secret managers** (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault)
- **CI/CD secret injection** (GitHub Actions secrets, GitLab CI variables)

## Generating Secrets

```bash
# JWT HMAC secret (32 bytes base64)
openssl rand -base64 32

# Webhook secret (32 bytes hex)
openssl rand -hex 32

# Generate a dev token (after setting COPILOT_JWT_HMAC_SECRET)
export COPILOT_JWT_HMAC_SECRET="your-secret"
go run gen_token.go
```

## FAQ

**Q: Why no config file?**  
A: The current design uses environment variables for 12-factor app compliance. A future PR could add Viper or similar for config file support.

**Q: How do I reload config without restarting?**  
A: Not supported yet. Changes require a pod restart. Consider adding SIGHUP reload in a future PR.

**Q: Can I use .env for production?**  
A: Not recommended. Use K8s ConfigMaps/Secrets or equivalent. The `.env` loader is for local dev convenience.
