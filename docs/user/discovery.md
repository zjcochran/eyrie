# Discovery

**Discover**, in the menu (admins only), lists what one source has: the
containers of a Docker engine, what an agent's platform runs, a network
device and its interfaces, or a NAS's storage. **Add monitor** on one of them opens the
monitor form, filled in, for you to check and save. Discovery runs only
when you ask, for one type at a time, and adds nothing by itself.

## Finding things

1. Choose what to **Find**.
2. Choose where it **Runs on**: **Eyrie server (local)**, or one of the
   [agents](agents.md) that can find it. Only those are offered; when none
   can, the page says to add one.
3. For a network device or a NAS, enter the device and its credentials
   (below).
4. **Discover** lists what was found, with how each one is now, leaving
   out what is monitored already (below).

| Find | Eyrie server (local) | Agents | What is listed | The monitor |
| --- | --- | --- | --- | --- |
| Docker containers | Eyrie's own Docker engine, at `unix:///var/run/docker.sock`: mount the socket into Eyrie's container, as for [Docker container](monitors.md#docker-container) monitors | Docker and Docker Swarm agents | Every container, running or not, with its Compose project | [Docker container](monitors.md#docker-container), by name |
| Docker Swarm services | No | Docker Swarm agents | The services, with their stack | [Docker Swarm service](monitors.md#docker-swarm-service-kubernetes-workload-proxmox-ve-guest) |
| Kubernetes workloads | No | Kubernetes agents | Deployments, StatefulSets and DaemonSets, with their namespace | [Kubernetes workload](monitors.md#docker-swarm-service-kubernetes-workload-proxmox-ve-guest) |
| Proxmox VE guests | No | Proxmox VE agents | LXC containers and VMs, with their node | [Proxmox VE guest](monitors.md#docker-swarm-service-kubernetes-workload-proxmox-ve-guest), by VM ID |
| Network devices (SNMP) | Asked from Eyrie's network | Any agent: asked from the agent's network | The device, and each of its interfaces | [SNMP](monitors.md#snmp) |
| NAS storage (SNMP) | Asked from Eyrie's network | Any agent: asked from the agent's network | Each volume's space and health, the disks' health and temperature, the system's health, each command snmpd runs | [SNMP storage](monitors.md#snmp-storage-nas) |

An agent's list is its last report, sent every 30 seconds. An agent that is
not reporting, or could not list its platform, says so instead:
`The agent edge is not reporting: start it, then try again.`

**Add monitor** opens **Add a monitor** with the type, the name, **Runs
on** and the settings of what was chosen; everything else has its usual
default. Change what you like, then **Add**.

What a monitor already checks is not listed: one of the same type, running
on the same place, of the same container, service, workload or guest, or
of the same SNMP host and OID, or of the same SNMP host, watch, volume,
disk and command. The list says how many it left out
(`Not listed: 3 monitored already.`), and when that is everything,
`Nothing new found.`

## Network devices (SNMP)

Enter the device as for an [SNMP monitor](monitors.md#snmp): **host**,
port (161), **SNMP version**, and the **community** (versions 1 and 2c) or
the version 3 user, authentication, privacy and context name. Eyrie, or
the agent, then asks the device, within 60 seconds:

- its name and what it is (SNMPv2-MIB `sysName` and `sysDescr`);
- its interfaces (IF-MIB): `ifDescr`, `ifAdminStatus` and `ifOperStatus`
  from `ifTable`, `ifName` and `ifAlias` from `ifXTable`, walking each
  column with GetNext requests, at most 1,000 interfaces.

The list is headed with the device's name and the first line of its
description, and offers:

| Monitor | Name | OID | Up when |
| --- | --- | --- | --- |
| The device | Its `sysName`, or the host without one | `1.3.6.1.2.1.1.3.0` (`sysUpTime`) | It answers, with any value |
| Each interface | The device's name and the interface's `ifName` (or `ifDescr`) | `1.3.6.1.2.1.2.2.1.8.<index>` (`ifOperStatus`) | The value is `1` (up) |

Each interface shows its state (up, down, lower layer down, …), `turned
off` when it is administratively down, and its description on the device
(`ifAlias`). The community and passwords you typed go into the monitor
form with the rest; they are stored only when you save the monitor, sealed
like every [secret setting](monitors.md#secret-settings), and kept only
in the browser's memory until then.

A wrong community gets no answer: `No answer: is the community right?`.
An agent from before discovery answers `The agent edge cannot discover:
update it to this version of Eyrie.`

Each discovery is recorded in the audit log (`discovery`, with the type,
where it ran and how many were found).

## NAS storage (SNMP)

Enter the NAS as for a network device. Eyrie, or the agent, asks it,
within 60 seconds, for its name and description (on Synology, its model
and DSM version), and for what [SNMP storage](monitors.md#snmp-storage-nas)
monitors watch, as [NAS monitoring](nas.md) describes. It offers:

| Monitor | Name | Watch | Up now when |
| --- | --- | --- | --- |
| Each volume's space | The device's name, the mount point and `space` | `capacity` | At most 90% is used |
| Each volume's or pool's health | The device's name, the volume and `health` | `volume-health` | It is healthy, or being repaired or scrubbed |
| The disks' health | The device's name and `disk health` | `disk-health`, all disks | Every disk is healthy |
| The disks' temperature | The device's name and `disk temperature` | `disk-temperature`, all disks | No disk is above 50 °C |
| The system's health | The device's name and `system health` | `system-health` | Nothing has failed |
| Each command snmpd runs | The device's name and the `extend` line's name | `command` | It exited with 0 |

The space table leaves out the mount points of the system's memory and
devices (`/dev`, `/run`, `/sys`, `/proc`, `/tmp` and below). An agent from
before NAS discovery answers `The agent edge cannot discover: update it
to this version of Eyrie.`

