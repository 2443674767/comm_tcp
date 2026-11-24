FROM scratch
COPY lanchat /lanchat
ENV DATA_DIR=/data
EXPOSE 18086/tcp 18087/udp 18085/tcp
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/lanchat", "healthcheck"]
ENTRYPOINT ["/lanchat"]
