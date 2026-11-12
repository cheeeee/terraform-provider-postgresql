ARG PGVERSION
FROM postgres:${PGVERSION:-latest}

ARG PGVERSION
RUN apt-get update && apt-get install -y build-essential postgresql-server-dev-${PGVERSION:-all}
# pgaudit is used to test the role / database parameters with an audit setup similar to AWS RDS
RUN apt-get install -y postgresql-${PG_MAJOR}-pgaudit
RUN dpkg -l |grep postgresql
COPY dummy_seclabel /opt/dummy_seclabel
WORKDIR /opt/dummy_seclabel
RUN make
