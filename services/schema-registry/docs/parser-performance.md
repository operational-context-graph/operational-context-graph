# Parser performance checks

Task 2.2 covers strict parsing and JSON Canonicalization Scheme (JCS) robustness.
These tests change no runtime limits or format semantics.
Fuzz targets, allocation benchmarks, and their shared helpers live in `test/benchmark`.
They exercise the exported strict JSON and manifest packages as black-box tests.

## Input profile

Inputs obey the existing serviceability profile:

| Dimension | Maximum |
| --- | --- |
| Document | 16777216 bytes |
| Nesting | 64 levels |
| Object members | 256 |
| Array entries | 10000 |
| Decoded string | 1048576 UTF-8 bytes |
| Exact integer magnitude | 9007199254740991 |

Workloads include exact boundaries and rejected overflow values.
Allocation benchmarks include dense 16 MiB documents and immediate byte-overflow rejection.
They include malformed UTF-8, escaped duplicates, extreme depth, broad transformation arrays, null conditions, huge strings, and numeric boundaries.
Null conditions remain valid JSON but invalid source-mapping descriptors.
These checks do not replace descriptor semantic tests.
Program-shaped metadata remains data; no program runs.

## Allocation regression budgets

Benchmarks MUST report `B/op` and `allocs/op`.
They MUST enforce these conservative per-operation budgets for the supplied workloads:

| Operation | Allocated bytes | Allocation count |
| --- | --- | --- |
| Strict parse | 1 MiB + 64 × input bytes | 4096 + 4 × input bytes |
| Strict gate plus JCS | 1 MiB + 128 × input bytes | 4096 + 8 × input bytes |
| Complete manifest parse | 1 MiB + 128 × input bytes | 4096 + 8 × input bytes |

Input construction and fixture loading occur outside measurement.
Counters measure total allocation, not peak resident memory or retained heap.
Benchmarks run serially because Go allocation counters cover the whole process.
Fixed headroom accommodates runtime noise and platform differences.
These budgets detect regressions in these workloads; they do not prove bounds for every possible document.
Fuzzing checks panic freedom, caller-byte preservation, successful round trips, and canonicalization idempotence.
The canonicalization fuzz target applies strict parsing before calling the JCS dependency, matching production behavior.

## Scoped verification

Run from `services/schema-registry`:

```sh
make test TEST_PACKAGES='./pkg/schema-artifacts/strictjson ./pkg/schema-artifacts/manifest ./test/benchmark'
make test-benchmark TEST_ARGS='-run=^$$ -bench=Profile -benchtime=1x -race'
make test TEST_PACKAGES='./test/benchmark' TEST_ARGS='-run=^$$ -fuzz=^FuzzStrictJSONParse$$ -fuzztime=10s -parallel=2'
make test TEST_PACKAGES='./test/benchmark' TEST_ARGS='-run=^$$ -fuzz=^FuzzManifestParse$$ -fuzztime=10s -parallel=2'
make test TEST_PACKAGES='./test/benchmark' TEST_ARGS='-run=^$$ -fuzz=^FuzzCanonicalize$$ -fuzztime=10s -parallel=2'
```

Increase fuzz duration for scheduled testing.
Keep fuzz workers bounded; each worker can process serviceability-boundary documents.
Double dollar signs preserve regular-expression anchors through Make expansion.
