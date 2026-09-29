# syntax=docker/dockerfile:1
#
# The relay serves the browser player from its own binary, embedded from source
# with no asset build, so this is one Go stage into distroless. There is no
# Node in the supply chain of the deployed artefact.
#
# The console is not in this image. It needs cgo for the miniaudio capture
# backend and it runs on an operator's machine, not on the droplet.

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
# CGO is off so the binary is static and the final image needs no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
EXPOSE 8080 9000
USER nonroot:nonroot
ENTRYPOINT ["/server"]
CMD ["--http-addr", ":8080", "--tcp-source-addr", ":9000"]
