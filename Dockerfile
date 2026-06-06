FROM debian:stable
COPY picobot /bin
RUN mkdir -p /root/.picobot/workspace

