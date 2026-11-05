FROM ghcr.io/aninsomniacy/aria2-next:latest

USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends jq curl \
 && rm -rf /var/lib/apt/lists/*

ENTRYPOINT ["/usr/local/bin/aria2-next"]
