# geul-transcoder

Go workers for audio/video transcoding and waveform generation. The two
executables share one image build:

- `cmd/transcoder` consumes `transcoder.audio` and `transcoder.video` jobs.
- `cmd/waveform-processor` consumes `waveform.generate` jobs.

The workers use the published Geul event contracts for queue and signal
payloads.

## Development

Go 1.26.6 and FFmpeg/FFprobe are required.

```sh
go mod download
go test -race -count=1 ./...
go vet ./...
go build ./cmd/transcoder
go build ./cmd/waveform-processor
```

Compose examples use `GEUL_*` host variables. Set at least
`GEUL_TRANSCODER_DATABASE_DSN`, `GEUL_S3_MEDIA_BUCKET`, `GEUL_S3_REGION`, and
the S3 credentials before starting a worker.

## Images

```sh
docker build -t geul-transcoder:local .
docker build --build-arg BUILD_TARGET=./cmd/waveform-processor \
  -t geul-waveform-processor:local .
```

Release images are published as:

- `registry.dsub.io/echovisionlab/geul-transcoder:vX.Y.Z`
- `registry.dsub.io/echovisionlab/geul-waveform-processor:vX.Y.Z`

Releases use Release Please and GitHub-hosted Actions. The initial release is
`v0.1.0`; production deployment configuration is maintained separately.

## License

PolyForm Noncommercial 1.0.0. Commercial use requires a separate license from
Echo Vision Lab. See [LICENSE.md](LICENSE.md).
