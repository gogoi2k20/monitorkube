FROM golang:1.23.1-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/monitorkube ./cmd/monitorkube

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/monitorkube /monitorkube
USER 65532:65532
ENTRYPOINT ["/monitorkube"]
