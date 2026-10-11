# evm-tx-sender

A small Go microservice that sends EVM transactions on behalf of callers across multiple networks and EOAs. Built as a hiring task in ~5.5 hours.

---

## Quickstart

```bash
# 1. Prepare env
cp .env.example .env
# edit CHAIN_RPC_URLS and PRIVATE_KEYS

# 2. Run
go run ./cmd/server
# or: make build && ./evm-tx-sender

# 3. Submit a transaction
curl -X POST http://localhost:8080/v1/transactions \
  -H 'Content-Type: application/json' \
  -d '{
    "network": 10,
    "sender":  "0xYourEOA",
    "to":      "0xDeadBeef...",
    "value":   "1000000000000000000"
  }'
# -> 201 Created with the full Job snapshot (jobId, nonce, first txHash, state)

# 4. Poll for the final receipt
curl http://localhost:8080/v1/transactions/<jobId>
```

Dev loop:
```bash
make test   # go test -race ./...
make build  # go build ./...
make lint   # golangci-lint
make genmocks
```

---

## HTTP API

### `POST /v1/transactions`

Submits a transaction and **blocks until the first broadcast attempt returns** (success or failure). Returns the full Job snapshot.

Request body:
```json
{
  "network": 10,
  "sender":  "0x...",
  "to":      "0x...",         // optional; omit for contract creation
  "value":   "1000000000",    // optional, decimal wei string
  "data":    "0x...",         // optional, hex
  "gasLimit": 50000,          // optional
  "maxFeePerGas":         "...",  // optional, decimal wei
  "maxPriorityFeePerGas": "..."   // optional, decimal wei
}
```

Response (201 Created):
```json
{
  "jobId":  "a1b2c3...",
  "status": "broadcast",
  "chainId": 10,
  "sender":  "0x...",
  "nonce":   42,
  "attempts": [
    { "hash": "0x...", "gasLimit": 50000, "tipCap": "...", "feeCap": "...", "sentAt": "..." }
  ],
  "receipt": null,
  "error":   "",
  "createdAt": "...",
  "updatedAt": "..."
}
```

### `GET /v1/transactions/{jobId}`

Returns the current snapshot of a Job. Clients poll this to see the receipt once mined.

### `GET /healthz`

Always 200 while the process is up.

### Error mapping

| Case | Status |
|---|---|
| Malformed JSON / bad address / bad hex / bad wei | 400 |
| Unknown chain or sender (no worker configured) | 400 |
| Worker intake buffer full | 429 |
| Caller context deadline | 504 |
| First broadcast errored unrecoverably (`Job.State == failed`) | 502 |
| Unknown jobId on GET | 404 |
| Anything else | 500 |


### Job states

- `broadcast` — sent, waiting to be mined
- `mined`    — receipt received, status 1
- `reverted` — receipt received, status 0
- `failed`   — broadcast errored unrecoverably (no state on-chain)
- `stuck`    — exceeded `MAX_BUMPS` without a receipt

---

## Architecture

### Static topology

```
                       ┌──────────────────────────────────┐
                       │        net/http + chi router     │
                       └────────────────┬─────────────────┘
                                        │
                                 api.Handler
                                        │
                         ┌──────────────┼──────────────┐
                         │              │              │
                         │   services map[uint64]Svc   │
                         │   registry  *JobRegistry    │
                         │              │              │
                         └──────┬───────┴───────┬──────┘
                                │               │
                        POST: Service.Submit    │
                                │               │
              ┌─────────────────┼──────────┐    │  GET: Registry.Get
              ▼                 ▼          ▼    │  (bypasses services)
       ┌─────────────┐   ┌─────────────┐        │
       │ Service(1)  │   │Service(137) │        │
       │ map[addr]→W │   │ map[addr]→W │        │
       └──┬───────┬──┘   └──┬───────┬──┘        │
          │       │         │       │           │
          ▼       ▼         ▼       ▼           │
       ┌─────┐ ┌─────┐   ┌─────┐ ┌─────┐        │
       │ W_A │ │ W_B │   │ W_A │ │ W_B │        │
       │c=1  │ │c=1  │   │c=137│ │c=137│        │
       └──┬──┘ └──┬──┘   └──┬──┘ └──┬──┘        │
          │      │          │      │             │
          └──────┴──────────┴──────┴──┐          │
                                      ▼          ▼
                             ┌───────────────────────┐
                             │     JobRegistry       │
                             │ jobs map[id]*Job      │
                             │ sync.RWMutex          │
                             └───────────────────────┘
```

- **One `Service` per chain** — holds a flat `map[common.Address]Worker` for that chain's wallets.
- **One `Worker` per `(chain, EOA)`** — long-lived goroutine, exclusive owner of its nonce stream.
- **Single `JobRegistry`** shared across all services — jobs are globally unique by UUID so GET works without a chain hint.

### Request lifecycle & concurrency

```
Client                   HTTP Server                  Worker (long-lived, one per EOA)
──────                   ───────────                  ────────────────────────────────
                     ┌──────────────────┐              ┌──────────────────────────┐
POST /v1/tx ────────►│ accept           │              │  for {                   │
                     │ spawn goroutine  │              │    select {              │
                     │     ──┐          │              │      case j := <-w.jobs: │
                     │       ▼          │              │        SubmitJob(j)      │
                     │  handler.submit  │              │        defer close(      │
                     │       │          │              │          j.done)         │
                     │  services[chain] │              │      case <-tick.C:      │
                     │       │          │              │        UpdateInflight()  │
                     │  service.Submit  │              │      case <-ctx.Done():  │
                     │       │          │              │        return           │
                     │  worker.Submit ──┼──► w.jobs    │    }                     │
                     │       │          │              │  }                       │
                     │  <-job.Done() ◄──┼─────────────┤                          │
                     │   (PARKED)       │              └──────────────────────────┘
                     │       │          │
                     │  registry.Get()  │
                     │       │          │
                     │  writeJSON(201)  │
                     │  goroutine exits │
                     └──────────────────┘
```

### Concurrency boundaries

| Scope | Parallelism |
|---|---|
| **Across chains** | Fully parallel (different Services, different Workers). |
| **Across wallets on one chain** | Fully parallel (different Workers, independent goroutines). |
| **Within one `(chain, wallet)`** | **Serial.** Jobs queue on the worker's intake channel; the single worker goroutine drains them one at a time. This is the design: nonce ordering requires serialization. |
| **GET requests** | Fully parallel. Go straight to the registry (its own RWMutex), bypassing services. |

Horizontal scaling = add more EOAs. Open for discussion but I'd prefer RPC-latency reduction + more EOAs over parallelising a single wallet.

---

## Design decisions

### Sync-broadcast
POST blocks until the first broadcast attempt returns and responds with `{jobId, nonce, txHash, state}`. Fire-and-forget was rejected(e.g. because every client would need to poll just to confirm the broadcast landed). Fully-sync (block until mined) was rejected (e.g. because it could hang the request for minutes). Mining stays async on the worker's tick — only the broadcast step is sync.

### One Service per chain, worker map per Service
`map[uint64]*Service` keyed by chainID; each Service holds `map[common.Address]*Worker` for that chain's wallets. New chains plug in via config without touching routing code. Opens new possibilities of different service behavior per chain, and different worker behavior per service. 

### Serial per worker, horizontal scaling
Each worker is a single goroutine running broadcasts sequentially. Potential improvement is making sure inflight txs are not blocking it but a part from that, I would argue we want different kinds of optimizations, e.g. RPC latency optimization, and horizontal scaling (more EOAs/workers) rather than trying to parallelize this. 

### Gas strategy — EIP-1559
EIP-1559 only; Caller can override `gasLimit` / `maxFeePerGas` / `maxPriorityFeePerGas` per request.

### Stuck transaction handling
After `MAX_BUMPS` without a receipt the job is marked `stuck` and and a specific error log is printed out for which an alert needs to be set up. This is a signal, not recovery. Full recovery needs periodic re-tracking of the tx (it may still mine or be evicted) plus an optional cancel-tx. A private mempool (e.g. Flashbots Protect) that emits explicit `dropped` events would replace the polling.

### Security
Private keys from env only; `PRIVATE_KEYS` is `unset` after read.
`Wallet.LogValue()` + `Wallet.String()` redact to the address — `slog` can't accidentally leak the key.

---

## What I'd add with more time

### Optional `sender` on submit
Make `sender` optional; when absent, pick a free worker (round-robin or least-loaded) for that chain. Current API forces the caller to pre-select a wallet.

### Persistent `JobRegistry`
In-memory only today — restart loses data (clients get 404 on GET), but nonces re-sync from chain so new submits are still correct.

### Private keys in a vault
Keys read from env (and the var is `unset` after load for crash-dump safety). A secrets manager (e.g.HashiCorp Vault) is the production next step.

### Per-chain config in YAML
`WorkerConfig` is applied globally today. Per-chain `tick`, `bump %`, `max_bumps` matter in practice. YAML / JSON per chain is cleaner than packing it into env vars.

### Idempotency-Key header
Not implemented. Shape: client sends `Idempotency-Key`, server stores `(key -> jobID + SHA256(body))`. Same key + same hash -> return existing job; same key + different hash -> error code. Prevents double-send on client retry.

### Request-ID in worker logs
chi adds a RequestID at the HTTP layer but it doesn't reach the worker (worker runs on its own context). Threading request-id + job-id into every worker log line would make it easier to trace one request end-to-end. Also, adding additional debug logs throughout the codebase would be encouraged.

### More unit + integration tests
More unit tests for all parts of the code, alongside possibly integration tests on a forked VM. 

### Prometheus metrics
Not implemented. Would give ops visibility into per-worker health and the signals to act on.

### Error handling on API
Error body is a flat `{"error": "..."}` for now. Structured errors are the next step for production.

### Auth middleware
Auth not implemented but would be needed before going to production using e.g. an API key per client using the service.

---