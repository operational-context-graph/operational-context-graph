# OCG Inventory Connector — Architecture Diagrams

## System Context

```mermaid
flowchart LR
    VAULT["Secrets Vault (OpenBao)"]
    NETBOX["NetBox REST API"]
    CONNECTOR["connectors/inventory"]
    DI["Data Ingestion IngestionService gRPC"]

    VAULT -- "cert+key (k8s mount)" --> CONNECTOR
    CONNECTOR -- "paginated GET (HTTPS + token)" --> NETBOX
    CONNECTOR -- "IngestionService.Ingest (gRPC / mTLS)" --> DI
```

> The connector delivers **raw source records** to Data Ingestion and stops there.
> Everything downstream — schema mapping, IRI construction, entity resolution — is the OCG's concern.

## Internal Components

```mermaid
flowchart TD
    MAIN["main.go\ncmd/connector"]

    CFG["Config\ninternal/config"]
    CLIENT["HTTPClient\ninternal/netbox"]
    ORCH["Connector\ninternal/runner"]
    ENV["Envelope builder\ninternal/envelope"]
    PUB["Publisher\ninternal/publisher"]

    GRPC["GRPCPublisher\nconn.go batch.go"]
    STUB["StubPublisher (tests only)"]

    MAIN --> CFG
    MAIN --> ORCH

    ORCH --> CLIENT
    ORCH --> ENV
    ORCH --> PUB

    PUB --> GRPC
    PUB --> STUB
```

## Run Sequence

```mermaid
sequenceDiagram
    participant Caller as Caller
    participant Main as main.go
    participant Runner as Connector (runner)
    participant NB as NetBox API
    participant Pub as GRPCPublisher
    participant DI as Data Ingestion

    Caller->>Main: start

    loop each endpoint
        Main->>Runner: ProcessEndpoint(ctx, endpoint)

        Runner->>NB: Paginate(ctx, endpoint)
        NB-->>Runner: []json.RawMessage

        note over Runner: Build(raw) to MessageEnvelope with payload, artifact id, watermark

        Runner->>Pub: Publish([]Message)
        Pub->>DI: IngestionService.Ingest(up to 1000 msgs / 4 MiB)
        DI-->>Pub: IngestResponse
        alt UNAVAILABLE
            Pub-->>Runner: back-pressure error
            Runner-->>Main: error
            Main->>Caller: exit 1
        end
    end

    Main->>Caller: exit 0 + summary log
```
