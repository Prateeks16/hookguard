# Build a static, zero-dependency gateway binary, then ship it on a minimal
# distroless base (CA certs + nonroot user, ~2MB). Final image is well under 50MB.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hookguard .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/hookguard /hookguard
COPY config.json /config.json
EXPOSE 9000
USER nonroot:nonroot
ENTRYPOINT ["/hookguard"]
