ARG MYSQL_VERSION

FROM mysql/mysql-server:${MYSQL_VERSION}
USER root
WORKDIR /work
ENV GOPATH=/go
ENV PATH=/go/bin:/usr/local/go/bin:"$PATH"

RUN yum install -y tar \
    && yum clean all

ARG GO_VERSION=1.15.7

RUN curl -s -f -o go.tar.gz https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz \
    && tar -x -z -C /usr/local -f go.tar.gz \
    && rm go.tar.gz \
    && mkdir -p $GOPATH/src/github.com/cybozu-go

RUN mkdir -p /etc/mysql \
    && chown -R mysql:mysql /etc/mysql \
    && mkdir -p /var/log/mysql \
    && chown -R mysql:mysql /var/log/mysql \
    && mkdir -p /home/mysql \
    && chown -R mysql:mysql /home/mysql

USER mysql
ENV HOME=/home/mysql

WORKDIR $GOPATH/src/github.com/cybozu-go/moco
