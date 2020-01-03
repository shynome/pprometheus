FROM golang:1.13-alpine as Build
COPY vendor /pp/vendor
COPY cmd    /pp/cmd
COPY web    /pp/web
COPY go.mod /pp/go.mod
WORKDIR /pp/cmd/prometheus
RUN go build -mod=vendor

FROM alpine
COPY --from=Build /pp/cmd/prometheus/prometheus  /bin/prometheus
COPY cmd/prometheus/prometheus.yml               /etc/prometheus/prometheus.yml
COPY cmd/prometheus/web                          /prometheus/web

RUN mkdir -p /prometheus && \
    chown -R nobody:nogroup etc/prometheus /prometheus

USER       nobody
EXPOSE     9090
VOLUME     [ "/prometheus" ]
WORKDIR    /prometheus
ENTRYPOINT [ "/bin/prometheus" ]
CMD        [ "--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.path=/prometheus" ]