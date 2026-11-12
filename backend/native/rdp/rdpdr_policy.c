#include "rdpdr_policy.h"

#include <string.h>

static unsigned int rd16(const unsigned char *p) {
 return (unsigned int)p[0] | ((unsigned int)p[1] << 8);
}
static uint32_t rd32(const unsigned char *p) {
 return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) |
        ((uint32_t)p[3] << 24);
}
static uint64_t rd64(const unsigned char *p) {
 return (uint64_t)rd32(p) | ((uint64_t)rd32(p + 4) << 32);
}
static void wr32(unsigned char *p, uint32_t v) {
 p[0] = (unsigned char)v;
 p[1] = (unsigned char)(v >> 8);
 p[2] = (unsigned char)(v >> 16);
 p[3] = (unsigned char)(v >> 24);
}

void nt_rdpdr_reset(NtRdpdrState *st) {
 if (!st) return;
 memset(st, 0, sizeof(*st));
}

static void event(NtRdpdrState *st, unsigned char op, unsigned char dir, uint32_t device,
                  uint32_t completion, uint32_t bytes, uint64_t offset, const char *name,
                  size_t nameLen) {
 if (st->event_n == NT_RDPDR_EVENT_CAP) return; /* bounded: silently drop */
 NtRdpdrEvent *e = &st->events[(st->event_begin + st->event_n) % NT_RDPDR_EVENT_CAP];
 st->event_n++;
 memset(e, 0, sizeof(*e));
 e->op = op;
 e->dir = dir;
 e->device = device;
 e->completion = completion;
 e->bytes = bytes;
 e->offset = offset;
 if (name && nameLen) {
  if (nameLen > sizeof(e->name) - 1) nameLen = sizeof(e->name) - 1;
  memcpy(e->name, name, nameLen);
 }
}

static NtRdpdrPending *pending_find(NtRdpdrState *st, unsigned int completion) {
 for (size_t i = 0; i < st->pending_n; i++)
  if (st->pending[i].completion == completion) return &st->pending[i];
 return NULL;
}

static void pending_insert(NtRdpdrState *st, unsigned int completion, unsigned char op,
                           uint32_t bytes) {
 if (st->pending_n == NT_RDPDR_PENDING_CAP) memmove(st->pending, st->pending + 1,
                                                    sizeof(NtRdpdrPending) *
                                                        (NT_RDPDR_PENDING_CAP - 1));
 else st->pending_n++;
 st->pending[st->pending_n - 1] = (NtRdpdrPending){completion, op, bytes};
}
static void pending_remove(NtRdpdrState *st, NtRdpdrPending *p) {
 if (!p) return;
 const size_t i = (size_t)(p - st->pending);
 memmove(st->pending + i, st->pending + i + 1,
         sizeof(NtRdpdrPending) * (st->pending_n - i - 1));
 st->pending_n--;
}

/* UTF-16LE path to capped UTF-8. Unpaired surrogates collapse to their low
 * half; audit text only. */
static void path_utf8(const unsigned char *u, size_t uLen, unsigned char *out, size_t outCap) {
 size_t o = 0;
 for (size_t i = 0; i + 1 < uLen && o + 4 < outCap; i += 2) {
  uint32_t c = (uint32_t)u[i] | ((uint32_t)u[i + 1] << 8);
  if (c >= 0xd800 && c <= 0xdbff && i + 3 < uLen) {
   const uint32_t lo = (uint32_t)u[i + 2] | ((uint32_t)u[i + 3] << 8);
   if (lo >= 0xdc00 && lo <= 0xdfff) {
    c = 0x10000 + ((c - 0xd800) << 10) + (lo - 0xdc00);
    i += 2;
   } else c &= 0x3ff;
  } else if (c >= 0xd800 && c <= 0xdfff)
   c &= 0x3ff;
  if (c < 0x80) out[o++] = (unsigned char)c;
  else if (c < 0x800) {
   out[o++] = (unsigned char)(0xc0 | (c >> 6));
   out[o++] = (unsigned char)(0x80 | (c & 0x3f));
  } else if (c < 0x10000) {
   out[o++] = (unsigned char)(0xe0 | (c >> 12));
   out[o++] = (unsigned char)(0x80 | ((c >> 6) & 0x3f));
   out[o++] = (unsigned char)(0x80 | (c & 0x3f));
  } else {
   out[o++] = (unsigned char)(0xf0 | (c >> 18));
   out[o++] = (unsigned char)(0x80 | ((c >> 12) & 0x3f));
   out[o++] = (unsigned char)(0x80 | ((c >> 6) & 0x3f));
   out[o++] = (unsigned char)(0x80 | (c & 0x3f));
  }
 }
 if (o == outCap) o = outCap - 1;
 out[o] = 0;
}

/* target→user: DeviceIoRequest IRPs. Returns 1 to forward, 0 to drop. */
int nt_rdpdr_gate_back(const NtRdpdrPolicy *p, NtRdpdrState *st, const unsigned char *io,
                       size_t len) {
 if (!st) return 0;
 if (!p || len < 4 || rd16(io) != NT_RDPDR_CTYP_CORE) return 0;
 const unsigned int pkt = rd16(io + 2);
 /* Negotiation PDUs never reach the running-state gate; anything unknown
  * from the target side passes: the relay state machines own them. */
 if (pkt != NT_PAKID_CORE_DEVICE_IOREQUEST) return 1;
 if (len < 24) return 0;
 const uint32_t device = rd32(io + 4), completion = rd32(io + 12),
                major = rd32(io + 16), minor = rd32(io + 20);
 if (major == NT_IRP_MJ_READ) {
  if (len < 36) return 0;
  const uint32_t bytes = rd32(io + 24);
  /* Fail closed after any overflow: the direction stays closed for the rest
   * of the session. */
  if (!p->upload || st->read_overflow || bytes > p->max_read_op ||
      st->read_total + (uint64_t)bytes > p->max_read_total) {
   if (st->read_overflow || !p->upload)
    event(st, NT_RDPDR_EV_DENIED_READ, 1, device, completion, bytes, rd64(io + 28), NULL, 0);
   else {
    event(st, NT_RDPDR_EV_OVERFLOW_READ, 1, device, completion, bytes, rd64(io + 28), NULL, 0);
    st->read_overflow = 1;
   }
   return 0;
  }
  if (pending_find(st, completion)) return 0;
  pending_insert(st, completion, NT_RDPDR_EV_READ, bytes);
  return 1;
 }
 if (major == NT_IRP_MJ_WRITE && minor == 0) {
  if (len < 36) return 0;
  const uint32_t bytes = rd32(io + 24);
  if (!p->download || st->write_overflow || bytes > p->max_write_op ||
      st->write_total + (uint64_t)bytes > p->max_write_total) {
   if (st->write_overflow || !p->download)
    event(st, NT_RDPDR_EV_DENIED_WRITE, 1, device, completion, bytes, rd64(io + 28), NULL, 0);
   else {
    event(st, NT_RDPDR_EV_OVERFLOW_WRITE, 1, device, completion, bytes, rd64(io + 28), NULL, 0);
    st->write_overflow = 1;
   }
   return 0;
  }
  /* The write payload travels inside this PDU; the budget is spent now. */
  st->write_total += (uint64_t)bytes;
  event(st, NT_RDPDR_EV_WRITE, 1, device, completion, bytes, rd64(io + 28), NULL, 0);
  return 1;
 }
 if (major == NT_IRP_MJ_CREATE) {
  if (len < 56) return 0;
  const uint32_t pathLen = rd32(io + 52);
  if (pathLen > len - 56 || (pathLen & 1)) return 0;
  char path[32];
  path_utf8(io + 56, pathLen, (unsigned char *)path, sizeof(path));
  event(st, NT_RDPDR_EV_CREATE, 1, device, completion, 0, 0, path, strlen(path));
  (void)minor;
  return 1;
 }
 return 1;
}

/* user→target: announcements and completions. Announcements keep filesystem
 * devices only; read completions pay recorded bytes. */
int nt_rdpdr_gate_front(const NtRdpdrPolicy *p, NtRdpdrState *st, unsigned char *io, size_t len,
                        size_t *outLen) {
 if (!st || !outLen) return 0;
 *outLen = len;
 if (!p || len < 4 || rd16(io) != NT_RDPDR_CTYP_CORE) return 0;
 const unsigned int pkt = rd16(io + 2);
 if (pkt == NT_PAKID_CORE_DEVICELIST_ANNOUNCE) {
  if (len < 8) return 0;
  uint32_t count = rd32(io + 4);
  if (count > (len - 8) / 20) return 0; /* each entry is at least 20 bytes */
  if (count == 0) {
   *outLen = 0;
   return 1;
  }
  unsigned char *src = io + 8;
  unsigned char *dst = io + 8;
  uint32_t kept = 0;
  for (uint32_t i = 0; i < count; i++) {
   if ((size_t)(src - io) + 20 > len) return 0;
   const uint32_t type = rd32(src), device = rd32(src + 4);
   const uint32_t dataLen = rd32(src + 16);
   if ((size_t)(src - io) + 20 + (size_t)dataLen > len) return 0;
   memmove(dst, src, 20 + (size_t)dataLen);
   if (type == NT_RDPDR_DTYP_FILESYSTEM) {
    kept++;
    dst += 20 + (size_t)dataLen;
    unsigned char label[9] = {0};
    /* PreferredDosName is 8 uppercase bytes; copy printable prefix */
    for (size_t j = 0; j < 8; j++)
     label[j] = (src[8 + j] >= 0x20 && src[8 + j] < 0x7f) ? src[8 + j] : 0;
    event(st, NT_RDPDR_EV_DEVICE, 2, device, 0, 0, 0, (char *)label, strlen((char *)label));
   }
   src += 20 + (size_t)dataLen;
  }
  if (kept != count) {
   wr32(io + 4, kept);
   *outLen = 8 + (size_t)(dst - (io + 8));
  }
  return 1;
 }
 if (pkt == NT_PAKID_CORE_DEVICE_IOCOMPLETION) {
  if (len < 24) return 0;
  const uint32_t device = rd32(io + 4), completion = rd32(io + 12);
  NtRdpdrPending *pend = pending_find(st, completion);
  if (!pend || pend->op != NT_RDPDR_EV_READ) return 1; /* other IRP responses */
  if (len < 28) return 0;
  const uint32_t declared = rd32(io + 24);
  /* A read answer may be short but never longer than the request; a larger or
   * malformed answer is treated as a policy violation. */
  if (declared > pend->bytes || declared > len - 28) {
   st->read_overflow = 1;
   pending_remove(st, pend);
   event(st, NT_RDPDR_EV_OVERFLOW_READ, 2, device, completion, declared, 0, NULL, 0);
   return 0;
  }
  pending_remove(st, pend);
  st->read_total += (uint64_t)declared;
  event(st, NT_RDPDR_EV_READ, 2, device, completion, declared, 0, NULL, 0);
  return 1;
 }
 return 1;
}

size_t nt_rdpdr_drain(NtRdpdrState *st, NtRdpdrEvent *out, size_t cap) {
 if (!st || !out || !cap) return 0;
 size_t n = 0;
 while (n < cap && st->event_n) {
  out[n++] = st->events[st->event_begin];
  st->event_begin = (st->event_begin + 1) % NT_RDPDR_EVENT_CAP;
  st->event_n--;
 }
 return n;
}
