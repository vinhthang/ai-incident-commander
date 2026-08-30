FROM debian:bookworm-slim
ARG TARGETARCH
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates git openssh-client curl && \
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" && \
    chmod +x kubectl && mv kubectl /usr/local/bin/ && \
    rm -rf /var/lib/apt/lists/*
COPY bin/incident-commander-${TARGETARCH} /app/incident-commander
EXPOSE 8085
CMD ["/app/incident-commander"]
