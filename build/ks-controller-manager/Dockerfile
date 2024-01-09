# Copyright 2020 The KubeSphere Authors. All rights reserved.
# Use of this source code is governed by an Apache license
# that can be found in the LICENSE file.

# Download dependencies
FROM alpine:3.18.3 as base_os_context

ENV OUTDIR=/out
RUN apk add --no-cache ca-certificates

# Add KS Core Helm Chart
COPY config/ks-core ${OUTDIR}/var/helm-charts/ks-core

# Build
FROM golang:1.20.7 as build_context

ENV OUTDIR=/out
RUN mkdir -p ${OUTDIR}/usr/local/bin/

WORKDIR /workspace
ADD . /workspace/

RUN make ks-controller-manager
RUN mv /workspace/bin/cmd/controller-manager ${OUTDIR}/usr/local/bin/


# Final Image
FROM alpine:3.18.3

COPY --from=base_os_context /out/ /
COPY --from=build_context /out/ /

WORKDIR /

EXPOSE 8443
CMD ["sh"]
