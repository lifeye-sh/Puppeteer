/* Native RDP client-drive policy: device announcement filter and per-operation
 * MS-RDPDR gate for the redirected client disks. Pure C with no FreeRDP
 * dependency so it can be unit-tested without the SDK.
 *
 * The proxy relays the rdpr static channel through its own client/server state
 * machines (enabled only for the dedicated drive profile). This module gates
 * the two running-state crossings:
 * - target→user DeviceIoRequest: create/read/write IRPs from the terminal
 *   server. READ (upload: target reads the user disk) and WRITE (download:
 *   target writes the user disk) are policy-gated with per-operation and
 *   cumulative byte limits; other IRPs pass.
 * - user→target announcements and DeviceIoCompletion: non-filesystem devices
 *   (printers, serial, parallel, smartcard, unknown types) are removed from
 *   announcements; read completions pay their returned bytes into the
 *   cumulative limits.
 *
 * Everything is denied by default; a disabled policy drops announcements and
 * IO requests so the target never sees user devices.
 */
#ifndef NT_RDPDR_POLICY_H
#define NT_RDPDR_POLICY_H
#include <stddef.h>
#include <stdint.h>

#define NT_RDPDR_NAME "rdpdr"

/* MS-RDPDR wire constants (FreeRDP include/freerdp/channels/rdpdr.h). */
#define NT_RDPDR_CTYP_CORE 0x4472
#define NT_PAKID_CORE_DEVICELIST_ANNOUNCE 0x4441
#define NT_PAKID_CORE_DEVICE_IOREQUEST 0x4952
#define NT_PAKID_CORE_DEVICE_IOCOMPLETION 0x4943
#define NT_IRP_MJ_CREATE 0x00000000
#define NT_IRP_MJ_READ 0x00000003
#define NT_IRP_MJ_WRITE 0x00000004
#define NT_RDPDR_DTYP_FILESYSTEM 0x00000008

typedef struct {
 int upload;    /* target may read the user's disks (IRP_MJ_READ) */
 int download;  /* target may write the user's disks (IRP_MJ_WRITE) */
 uint32_t max_read_op;   /* per-request limit for IRP_MJ_READ */
 uint32_t max_write_op;  /* per-request limit for IRP_MJ_WRITE */
 uint64_t max_read_total;   /* cumulative delivered bytes, both gates */
 uint64_t max_write_total;
} NtRdpdrPolicy;

typedef struct {
 unsigned char op; /* see NtRdpdrEventOp */
 unsigned char dir; /* 1 = from target, 2 = from user */
 uint32_t device;
 uint32_t completion;
 uint32_t bytes;  /* requested or delivered bytes; 0 for metadata ops */
 uint64_t offset; /* read/write offsets */
 unsigned char name[32]; /* device label or create path, NUL-terminated */
} NtRdpdrEvent;

enum NtRdpdrEventOp {
 NT_RDPDR_EV_DEVICE = 1, /* device kept in an announcement */
 NT_RDPDR_EV_CREATE = 2, /* target opened a path on a user device */
 NT_RDPDR_EV_READ = 3,
 NT_RDPDR_EV_WRITE = 4,
 NT_RDPDR_EV_DENIED_READ = 5,
 NT_RDPDR_EV_DENIED_WRITE = 6,
 NT_RDPDR_EV_OVERFLOW_READ = 7,
 NT_RDPDR_EV_OVERFLOW_WRITE = 8
};

#define NT_RDPDR_EVENT_CAP 256
#define NT_RDPDR_PENDING_CAP 128

typedef struct {
 unsigned int completion;
 unsigned char op; /* 3 = read pending (completion delivers bytes) */
 uint32_t bytes;   /* requested read size */
} NtRdpdrPending;

typedef struct {
 NtRdpdrEvent events[NT_RDPDR_EVENT_CAP];
 size_t event_n, event_begin;
 NtRdpdrPending pending[NT_RDPDR_PENDING_CAP];
 size_t pending_n;
 uint64_t read_total, write_total;
 int read_overflow, write_overflow;
} NtRdpdrState;

void nt_rdpdr_reset(NtRdpdrState *st);
/* 1 = pass, 0 = drop. Events are appended for audit; the ring is bounded and
 * drops silently past NT_RDPDR_EVENT_CAP. */
int nt_rdpdr_gate_back(const NtRdpdrPolicy *p, NtRdpdrState *st, const unsigned char *io,
                       size_t len);
/* May shorten io (device list filter). *outLen receives the new length when
 * the return value is 1. */
int nt_rdpdr_gate_front(const NtRdpdrPolicy *p, NtRdpdrState *st, unsigned char *io, size_t len,
                        size_t *outLen);
/* Moves pending events out; returns the number written. */
size_t nt_rdpdr_drain(NtRdpdrState *st, NtRdpdrEvent *out, size_t cap);

#endif
