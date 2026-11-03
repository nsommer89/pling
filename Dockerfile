# plingd container image: static binary on distroless, runs as non-root.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/plingd ./cmd/plingd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/plingd /plingd
USER nonroot:nonroot
EXPOSE 8080
ENV PLING_LISTEN=:8080 PLING_USERS=/config/users.yaml
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/plingd", "-healthcheck"]
ENTRYPOINT ["/plingd"]
