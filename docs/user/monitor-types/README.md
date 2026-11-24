# Monitor types

A setup guide for each monitor type: what to prepare on the thing it
checks, how to fill in the form, what a working check says, and what to do
when it is down. [Monitors](../monitors.md) covers what every type has in
common: the dashboard, the [settings every
type has](../monitors.md#settings-every-type-has), secret settings, how the
status is decided, tags and history.

## Websites and APIs

| Type | Up when |
| --- | --- |
| [HTTP(S)](http.md) | A URL answers with an accepted status code |
| [HTTP(S) keyword](keyword.md) | A page contains a word, or does not |
| [HTTP(S) JSON query](json-query.md) | A JSON answer has the right value |
| [HTTP(S) in a real browser](real-browser.md) | A page loads in a real Chrome, with its scripts |
| [WebSocket upgrade](websocket-upgrade.md) | A WebSocket accepts a connection |
| [gRPC keyword](grpc-keyword.md) | A gRPC method answers, with a keyword |
| [Globalping](globalping.md) | A probe elsewhere in the world reaches the target |

## Network

| Type | Up when |
| --- | --- |
| [Ping](ping.md) | A host answers pings |
| [TCP port](port.md) | A TCP port accepts connections |
| [DNS](dns.md) | A DNS server answers with the right records |
| [SNMP](snmp.md) | A device's SNMP value is right |
| [SNMP storage (NAS)](snmp-storage.md) | A NAS's volumes, disks and system are healthy |
| [Tailscale ping](tailscale-ping.md) | A node of your tailnet answers |
| [WAN Drawbridge](drawbridge.md) | The internet connection has not failed over to a backup ISP |

## Servers and services

| Type | Up when |
| --- | --- |
| [SMTP](smtp.md) | A mail server answers, with TLS |
| [NTP](ntp.md) | A time server answers with good time |
| [RADIUS](radius.md) | A RADIUS server accepts a sign-in |
| [SIP OPTIONS](sip-options.md) | A SIP server answers |
| [SFTP](sftp.md) | An SFTP server accepts a sign-in |
| [Steam game server](steam.md) | A Valve game server answers |
| [Game server](gamedig.md) | A Minecraft, Valve or Quake server answers |

## Containers and hosts

| Type | Up when |
| --- | --- |
| [Docker container](docker.md) | A container runs and is healthy |
| [Docker Swarm service](docker-swarm.md) | Every task of a service runs (on an agent) |
| [Kubernetes workload](kubernetes.md) | Every replica is ready (on an agent) |
| [Proxmox VE guest](proxmox.md) | An LXC container or VM runs (on an agent) |
| [System resources](system-resources.md) | A Linux host's CPU and memory use are under their limits |
| [System service](system-service.md) | A systemd or Windows service runs |
| [PM2 process](pm2.md) | A PM2 process is online |

## Databases and message brokers

| Type | Up when |
| --- | --- |
| [PostgreSQL](postgres.md) | A query runs |
| [MySQL / MariaDB](mysql.md) | A query runs |
| [Microsoft SQL Server](sqlserver.md) | A query runs |
| [Oracle Database](oracledb.md) | A query runs |
| [MongoDB](mongodb.md) | A command runs, with the right value |
| [Redis](redis.md) | Redis answers PING |
| [MQTT](mqtt.md) | A topic has the right message |
| [RabbitMQ](rabbitmq.md) | A node has no alarms |
| [Kafka producer](kafka-producer.md) | A topic takes a message |

## Not checked by Eyrie

| Type | Up when |
| --- | --- |
| [Push](push.md) | A job calls the monitor's URL in time |
| [Manual](manual.md) | An admin sets it up or down |
