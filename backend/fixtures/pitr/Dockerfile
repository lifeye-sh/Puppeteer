FROM postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
RUN apk add --no-cache pgbackrest python3 && mkdir -p /drill && chown postgres:postgres /drill
COPY drill.sh /usr/local/bin/r11-pitr-drill
RUN chmod 755 /usr/local/bin/r11-pitr-drill
USER postgres
ENTRYPOINT ["/usr/local/bin/r11-pitr-drill"]
