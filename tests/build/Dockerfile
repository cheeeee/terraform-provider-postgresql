ARG PGVERSION
FROM postgres:${PGVERSION:-latest}

ARG PGVERSION
# PG_MAJOR is set by the postgres image, PGVERSION can also be a pre-release (e.g.: 19rc1)
RUN apt-get update && apt-get install -y build-essential postgresql-server-dev-${PG_MAJOR}
# pgaudit is used to test the role / database parameters with an audit setup similar to AWS RDS
RUN apt-get install -y postgresql-${PG_MAJOR}-pgaudit
RUN dpkg -l |grep postgresql
COPY dummy_seclabel /opt/dummy_seclabel
WORKDIR /opt/dummy_seclabel
RUN make
