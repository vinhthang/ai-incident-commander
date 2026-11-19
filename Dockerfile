FROM debian:bookworm-slim
ARG TARGETARCH
ENV HELM_VERSION=v3.21.4
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates git openssh-client curl && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" && \
    chmod +x kubectl && mv kubectl /usr/local/bin/kubectl && \
    curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" | tar -xz -C /tmp && \
    mv "/tmp/linux-${TARGETARCH}/helm" /usr/local/bin/helm && chmod +x /usr/local/bin/helm && \
    rm -rf "/tmp/linux-${TARGETARCH}" /var/lib/apt/lists/*
COPY bin/incident-commander-${TARGETARCH} /app/incident-commander
EXPOSE 8085
RUN chmod +x /app/incident-commander
CMD ["/app/incident-commander"]
