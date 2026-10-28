FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
COPY assets /src/assets
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/skillhub ./cmd/skillhub && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/skillhub-server ./cmd/skillhub-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates git
RUN mkdir /data && chmod 700 /data && chown 10001:10001 /data
COPY --from=build /out/skillhub /usr/local/bin/skillhub
COPY --from=build /out/skillhub-server /usr/local/bin/skillhub-server
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["skillhub-server"]
CMD ["--data-dir", "/data", "--listen", "0.0.0.0:8080", "--allow-http"]
