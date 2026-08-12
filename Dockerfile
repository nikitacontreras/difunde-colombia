# Sintaxis: https://docs.docker.com/build/building/multistage/
# Etapa 1: compilar el binario. h3-go embebe libh3 en C, así que el
# binario final es dinámico contra musl.
FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/cd-server ./cmd/server

# Etapa 2: imagen mínima de ejecución.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata libgcc
WORKDIR /app
COPY --from=build /out/cd-server /app/cd-server
COPY data/asn/ip2asn-co.csv /app/data/asn/ip2asn-co.csv
COPY data/asn_operator_mapping.csv /app/data/asn_operator_mapping.csv
EXPOSE 8080
USER nobody
ENTRYPOINT ["/app/cd-server"]
CMD ["serve"]
