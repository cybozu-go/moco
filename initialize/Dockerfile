ARG MYSQL_VERSION
FROM quay.io/cybozu/moco-mysql:${MYSQL_VERSION}

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

RUN mkdir -p /etc/mysql \
    && chown 10000:10000 -R /etc/mysql \
    && mkdir -p /var/lib/mysql \
    && chown 10000:10000 -R /var/lib/mysql \
    && mkdir -p /var/log/mysql \
    && chown 10000:10000 -R /var/log/mysql \
    && mkdir -p /var/run/mysqld \
    && chown 10000:10000 -R /var/run/mysqld \
    && mkdir -p /home/mysql \
    && chown 10000:10000 -R /home/mysql

ENV HOME /home/mysql
USER 10000
WORKDIR $GOPATH/src/github.com/cybozu-go/moco

ENTRYPOINT [""]
