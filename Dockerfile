FROM golang:1.27-bookworm AS test
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...

FROM test AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /nebulakv ./cmd/nebulakv \
    && mkdir -p /data

FROM scratch
COPY --from=build /nebulakv /nebulakv
COPY --from=build --chown=65532:65532 /data /data
USER 65532:65532
EXPOSE 6380
ENTRYPOINT ["/nebulakv"]
CMD ["--host", "0.0.0.0", "--data", "/data"]
