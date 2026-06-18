FROM debian:stable
COPY gino /bin
RUN mkdir -p /root/.gino/workspace

