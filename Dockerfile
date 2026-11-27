FROM golang:1.26-bookworm AS backend-build
WORKDIR /src/backend
COPY backend/ ./
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/app && CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=backend-build /out/server ./server
COPY --from=backend-build /out/worker ./worker
USER 10001:10001
EXPOSE 8080 9091
CMD ["/app/server"]
