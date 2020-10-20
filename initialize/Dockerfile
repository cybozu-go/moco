FROM quay.io/cybozu/moco-mysql:8.0.20.4

ENV GOPATH=/go
ENV PATH=/go/bin:/usr/local/go/bin:"$PATH"

ARG GO_VERSION=1.13.15
USER root
WORKDIR /work
RUN curl -s -f -O https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz \
    && rm -rf /usr/local/go \
    && tar -x -z -C /usr/local -f go${GO_VERSION}.linux-amd64.tar.gz \
    && rm go${GO_VERSION}.linux-amd64.tar.gz \
    && mkdir -p $GOPATH/src/github.com/cybozu-go

WORKDIR $GOPATH/src/github.com/cybozu-go/moco

ENTRYPOINT [""]
