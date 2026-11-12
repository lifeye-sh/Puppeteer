FROM postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
RUN apk add --no-cache pgbackrest && mkdir -p /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest && chown postgres:postgres /var/lib/pgbackrest /var/log/pgbackrest /var/spool/pgbackrest
COPY pgbackrest.conf /etc/pgbackrest/pgbackrest.conf
COPY pgbackrest-file /usr/local/bin/pgbackrest-file
RUN chmod 755 /usr/local/bin/pgbackrest-file
