# Build from this directory:
#   docker build -t mustard/customer-data:dev .

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src

# Download modules first so they are cached independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The schema and tax data are embedded, so the binary is self-contained.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server .

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server

# DATABASE_URL and CORS_ALLOWED_ORIGINS are supplied at runtime.
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
