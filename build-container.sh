#!/bin/bash

docker rmi $(docker images -q --filter 'dangling=true') 2> /dev/null || true #clean up untagged images
docker build -t docker-ci-builder -f ./build-Dockerfile . && \
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock docker-ci-builder
docker rmi $(docker images -q --filter 'dangling=true') 2> /dev/null || true #clean up untagged images
echo "Done"
