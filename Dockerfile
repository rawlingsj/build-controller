FROM scratch

ENTRYPOINT ["/build-controller"]

COPY ./bin/build-controller-linux-amd64 /build-controller