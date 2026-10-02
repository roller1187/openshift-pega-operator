FROM registry.redhat.io/openshift4/ose-operator-registry-rhel9:v4.17 AS builder

COPY catalog /configs

RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

FROM registry.redhat.io/openshift4/ose-operator-registry-rhel9:v4.17

COPY --from=builder /configs /configs
COPY --from=builder /tmp/cache /tmp/cache

EXPOSE 50051

ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]

LABEL operators.operatorframework.io.index.configs.v1=/configs
