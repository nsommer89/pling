# plingd container image: static binary on distroless, runs as non-root.
# Base images are pinned by digest; bump them on purpose (docker pull, then
# docker image inspect --format "{{index .RepoDigests 0}}").
FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/plingd ./cmd/plingd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/plingd /plingd
USER nonroot:nonroot
EXPOSE 8080
ENV PLING_LISTEN=:8080 PLING_USERS=/config/users.yaml
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/plingd", "-healthcheck"]
ENTRYPOINT ["/plingd"]
