/* Standalone unit test for the client-drive policy module. Built with plain
 * gcc; compiled and executed during the nt-native-rdp-worker image build.
 * cc -o rdpdr_test rdpdr_policy.c rdpdr_policy_test.c && ./rdpdr_test */
#include "rdpdr_policy.h"

#include <stdio.h>
#include <string.h>

static int failures;
static void expect(int got, int want, const char *what) {
 if (got != want) {
  fprintf(stderr, "FAIL %s: got %d want %d\n", what, got, want);
  failures++;
 }
}
static void expect_u64(uint64_t got, uint64_t want, const char *what) {
 if (got != want) {
  fprintf(stderr, "FAIL %s: got %llu want %llu\n", what, (unsigned long long)got,
          (unsigned long long)want);
  failures++;
 }
}
static void wr16(unsigned char *p, unsigned v) {
 p[0] = (unsigned char)v;
 p[1] = (unsigned char)(v >> 8);
}
static void wr32a(unsigned char *p, uint32_t v) {
 p[0] = (unsigned char)v;
 p[1] = (unsigned char)(v >> 8);
 p[2] = (unsigned char)(v >> 16);
 p[3] = (unsigned char)(v >> 24);
}
static void wr64a(unsigned char *p, uint64_t v) {
 wr32a(p, (uint32_t)v);
 wr32a(p + 4, (uint32_t)(v >> 32));
}
static void core(unsigned char *p, unsigned pkt) {
 wr16(p, 0x4472);
 wr16(p + 2, pkt);
}
static NtRdpdrPolicy pol(int upload, int download, uint32_t op, uint64_t total) {
 NtRdpdrPolicy p = {upload, download, op, op, total, total};
 return p;
}
static uint32_t rd32c(const unsigned char *p) {
 return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) |
        ((uint32_t)p[3] << 24);
}

/* Device announce entry: type(4) id(4) dos name(8) data length(4) data(4). */
static unsigned char *mk_entry(unsigned char *p, uint32_t type, uint32_t id, const char *name,
                               uint32_t dataLen, uint32_t data) {
 wr32a(p, type);
 wr32a(p + 4, id);
 memset(p + 8, 0, 8);
 if (name) memcpy(p + 8, name, strlen(name) > 8 ? 8 : strlen(name));
 wr32a(p + 16, dataLen);
 wr32a(p + 20, data);
 return p + 20 + dataLen;
}

static unsigned char announce_buf[512];
static size_t announce_len;
/* count is written explicitly so malformed counts can be exercised too */
static unsigned char *announce_setup(uint32_t count, const unsigned char *entries, size_t n) {
 core(announce_buf, 0x4441);
 wr32a(announce_buf + 4, count);
 if (n) memcpy(announce_buf + 8, entries, n);
 announce_len = 8 + n;
 return announce_buf;
}

static void test_device_filter(void) {
 NtRdpdrPolicy p = pol(1, 1, 1 << 20, 1ull << 30);
 NtRdpdrState st;
 memset(&st, 0, sizeof(st));
 unsigned char entries[128];
 unsigned char *w = entries;
 w = mk_entry(w, 0x20, 7, NULL, 4, 1);    /* smartcard */
 w = mk_entry(w, 0x08, 11, "C", 4, 2);    /* filesystem */
 w = mk_entry(w, 0x04, 13, NULL, 4, 3);   /* printer */
 const size_t n = (size_t)(w - entries);
 unsigned char *buf = announce_setup(3, entries, n);
 size_t out = 0;
 expect(nt_rdpdr_gate_front(&p, &st, buf, announce_len, &out), 1, "announce kept fs");
 expect_u64(out, 8 + (20 + 4), "announce rewritten length");
 expect(rd32c(buf + 4), 1, "announce count");
 expect(rd32c(buf + 8 + 4), 11, "kept device id");
 NtRdpdrEvent ev[8];
 size_t got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 1, "device event");
 expect_u64(strlen((char *)ev[0].name), 1, "device label length");
 expect(ev[0].name[0], 'C', "device label C");
 expect(ev[0].device, 11, "device id in event");

 /* every entry forbidden: rewritten to an empty announcement */
 memset(&st, 0, sizeof(st));
 w = entries;
 w = mk_entry(w, 0x20, 7, NULL, 4, 9);  /* smartcard */
 w = mk_entry(w, 0x04, 13, NULL, 4, 3); /* printer */
 const size_t n2 = (size_t)(w - entries);
 buf = announce_setup(2, entries, n2);
 out = 0;
 expect(nt_rdpdr_gate_front(&p, &st, buf, announce_len, &out), 1, "empty announce");
 expect_u64(out, 8, "empty announce length");
 expect(rd32c(buf + 4), 0, "empty announce count");

 /* malformed counts and foreign components dropped */
 memset(&st, 0, sizeof(st));
 buf = announce_setup(99, NULL, 0);
 expect(nt_rdpdr_gate_front(&p, &st, buf, announce_len, &out), 0, "count beyond data");
 wr16(buf, 0x5052);
 wr16(buf + 2, 0x4441);
 expect(nt_rdpdr_gate_front(&p, &st, buf, announce_len, &out), 0, "print component");
}

/* DeviceIoRequest: header(4) device(4) file(4) completion(4) major(4) minor(4),
 * then read/write add length(4) offset(8) and create adds its own fields. */
static void io_request(unsigned char *p, uint32_t device, uint32_t completion, uint32_t major,
                       uint32_t minor, uint32_t bytes, uint64_t offset) {
 core(p, 0x4952);
 wr32a(p + 4, device);
 wr32a(p + 8, 0x11111111);
 wr32a(p + 12, completion);
 wr32a(p + 16, major);
 wr32a(p + 20, minor);
 if (major == NT_IRP_MJ_READ || major == NT_IRP_MJ_WRITE) {
  wr32a(p + 24, bytes);
  wr64a(p + 28, offset);
 } else if (major == NT_IRP_MJ_CREATE) {
  wr32a(p + 24, 0x120089);
  wr64a(p + 28, 0);
  wr32a(p + 36, 0x80);
  wr32a(p + 40, 7);
  wr32a(p + 44, 1);
  wr32a(p + 48, 0);
  /* UTF-16LE path "\C:" → "\C:" as bytes 5c 00 43 00 3a 00 */
  const unsigned char path[] = {0x5c, 0, 0x43, 0, 0x3a, 0};
  wr32a(p + 52, sizeof(path));
  memcpy(p + 56, path, sizeof(path));
 }
}

static void test_back(void) {
 unsigned char buf[128];
 NtRdpdrEvent ev[8];
 size_t got;

 /* read within limits passes and registers a pending */
 NtRdpdrPolicy p = pol(1, 1, 1 << 20, 1ull << 30);
 NtRdpdrState st;
 memset(&st, 0, sizeof(st));
 io_request(buf, 5, 1001, NT_IRP_MJ_READ, 0, 65536, 4096);
 expect(nt_rdpdr_gate_back(&p, &st, buf, 36), 1, "read request");
 expect_u64(st.pending_n, 1, "read pending registered");
 expect_u64(st.read_total, 0, "read pays nothing until delivered");

 /* over per-op limit denied with an audit event; the direction fails closed */
 io_request(buf, 5, 1002, NT_IRP_MJ_READ, 0, (1 << 20) + 1, 0);
 expect(nt_rdpdr_gate_back(&p, &st, buf, 36), 0, "read over op limit");
 got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 1, "overflow read event");
 expect(ev[0].op, NT_RDPDR_EV_OVERFLOW_READ, "overflow op");
 expect(ev[0].completion, 1002, "overflow completion");
 io_request(buf, 5, 1003, NT_IRP_MJ_READ, 0, 8, 0);
 expect(nt_rdpdr_gate_back(&p, &st, buf, 36), 0, "reads fail closed");

 /* policy off denies reads */
 NtRdpdrPolicy off = pol(0, 1, 1 << 20, 1ull << 30);
 io_request(buf, 5, 1004, NT_IRP_MJ_READ, 0, 4096, 0);
 expect(nt_rdpdr_gate_back(&off, &st, buf, 36), 0, "read without policy");

 /* write pays the budget at request time; equal to the cap still fits */
 NtRdpdrPolicy w = pol(1, 1, 0, 0);
 w.max_read_op = 1 << 20;
 w.max_read_total = 1ull << 30;
 w.max_write_op = 65536;
 w.max_write_total = 200000;
 memset(&st, 0, sizeof(st));
 io_request(buf, 9, 2001, NT_IRP_MJ_WRITE, 0, 65536, 16);
 expect(nt_rdpdr_gate_back(&w, &st, buf, 36), 1, "write request");
 expect_u64(st.write_total, 65536, "write counted");
 io_request(buf, 9, 2002, NT_IRP_MJ_WRITE, 0, 65536, 0);
 expect(nt_rdpdr_gate_back(&w, &st, buf, 36), 1, "second write");
 expect_u64(st.write_total, 131072, "write cumulative");
 got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 2, "write events");

 /* write beyond the session cap denied, then fail closed */
 memset(&st, 0, sizeof(st));
 NtRdpdrPolicy capped = pol(1, 1, 0, 0);
 capped.max_write_op = 100000;
 capped.max_write_total = 150000;
 io_request(buf, 9, 3001, NT_IRP_MJ_WRITE, 0, 100000, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 1, "capped write 1");
 io_request(buf, 9, 3002, NT_IRP_MJ_WRITE, 0, 50000, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 1, "write exactly to cap");
 expect_u64(st.write_total, 150000, "write total at cap");
 io_request(buf, 9, 3003, NT_IRP_MJ_WRITE, 0, 1, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 0, "write one over cap");
 got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 3, "two writes plus one overflow event");
 expect(ev[2].op, NT_RDPDR_EV_OVERFLOW_WRITE, "overflow op");
 expect(ev[2].completion, 3003, "overflow completion");
 io_request(buf, 9, 3004, NT_IRP_MJ_WRITE, 0, 1, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 0, "write after overflow denied");

 /* create passes and audits the UTF-16 path converted to UTF-8 */
 memset(&st, 0, sizeof(st));
 NtRdpdrPolicy full = pol(1, 1, 1 << 20, 1ull << 30);
 io_request(buf, 5, 4001, NT_IRP_MJ_CREATE, 0, 0, 0);
 expect(nt_rdpdr_gate_back(&full, &st, buf, 64), 1, "create request");
 got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 1, "create event");
 expect(ev[0].op, NT_RDPDR_EV_CREATE, "create op");
 expect(ev[0].name[0], (unsigned char)0x5c, "create path byte 0");
 expect(ev[0].name[1], (unsigned char)'C', "create path byte 1");
 expect(ev[0].name[2], (unsigned char)':', "create path byte 2");

 /* metadata IRPs and negotiation PDUs pass; truncated requests drop */
 io_request(buf, 5, 0, 5, 0, 0, 0); /* IRP_MJ_QUERY_INFORMATION */
 expect(nt_rdpdr_gate_back(&full, &st, buf, 24), 1, "metadata irp");
 core(buf, 0x5350);
 expect(nt_rdpdr_gate_back(&full, &st, buf, 4), 1, "server capability");
 core(buf, 0x4952);
 expect(nt_rdpdr_gate_back(&full, &st, buf, 10), 0, "truncated io request");
}

/* DeviceIoCompletion: header(4) device(4) file(4) completion(4) iostatus(4),
 * then a read answer adds length(4) followed by the data. */
static void completion(unsigned char *p, uint32_t device, uint32_t comp, uint32_t status,
                       uint32_t length, size_t *len) {
 core(p, 0x4943);
 wr32a(p + 4, device);
 wr32a(p + 8, 0x22222222);
 wr32a(p + 12, comp);
 wr32a(p + 16, status);
 if (length > 0) {
  wr32a(p + 24, length);
  memset(p + 28, 0xaa, length);
  *len = 28 + length;
 } else {
  *len = 24;
 }
}

static void test_completions(void) {
 unsigned char buf[131200];
 memset(buf, 0, sizeof(buf));
 NtRdpdrEvent ev[8];

 /* read request then a matching short answer pays only its actual bytes */
 NtRdpdrPolicy p = pol(1, 1, 65536, 1 << 20);
 NtRdpdrState st;
 memset(&st, 0, sizeof(st));
 io_request(buf, 5, 5001, NT_IRP_MJ_READ, 0, 1000, 0);
 expect(nt_rdpdr_gate_back(&p, &st, buf, 36), 1, "read request");
 size_t len;
 completion(buf, 5, 5001, 0, 400, &len);
 size_t out = 0;
 expect(nt_rdpdr_gate_front(&p, &st, buf, len, &out), 1, "short read completion");
 expect_u64(st.read_total, 400, "delivered bytes paid");
 expect_u64(st.pending_n, 0, "pending cleared");
 expect_u64(out, 28 + 400, "completion length");
 size_t got = nt_rdpdr_drain(&st, ev, 8);
 expect_u64(got, 1, "read completion event");
 expect(ev[0].op, NT_RDPDR_EV_READ, "completion op");
 expect(ev[0].bytes, 400, "completion bytes");

 /* an answer larger than the request is a violation: fail closed */
 memset(&st, 0, sizeof(st));
 NtRdpdrPolicy capped = pol(1, 1, 1000, 2000);
 io_request(buf, 5, 6001, NT_IRP_MJ_READ, 0, 1000, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 1, "capped read request");
 completion(buf, 5, 6001, 0, 100000, &len);
 expect(nt_rdpdr_gate_front(&capped, &st, buf, len, &out), 0, "oversized answer dropped");
 expect_u64(st.read_total, 0, "nothing paid");
 expect(st.read_overflow, 1, "overflow flagged");
 io_request(buf, 5, 6002, NT_IRP_MJ_READ, 0, 10, 0);
 expect(nt_rdpdr_gate_back(&capped, &st, buf, 36), 0, "reads fail closed after overflow");

 /* reaching the session cap after delivery is denied at the next request */
 memset(&st, 0, sizeof(st));
 NtRdpdrPolicy cap2 = pol(1, 1, 0, 0);
 cap2.max_read_op = 65536;
 cap2.max_read_total = 2000;
 io_request(buf, 5, 7001, NT_IRP_MJ_READ, 0, 2000, 0);
 expect(nt_rdpdr_gate_back(&cap2, &st, buf, 36), 1, "request within cap");
 completion(buf, 5, 7001, 0, 2000, &len);
 expect(nt_rdpdr_gate_front(&cap2, &st, buf, len, &out), 1, "deliver full cap");
 expect_u64(st.read_total, 2000, "cap delivered");
 io_request(buf, 5, 7002, NT_IRP_MJ_READ, 0, 1, 0);
 expect(nt_rdpdr_gate_back(&cap2, &st, buf, 36), 0, "request beyond cap denied");

 /* write completions and unmatched completions pass unchanged */
 memset(&st, 0, sizeof(st));
 completion(buf, 5, 99999, 0, 0, &len);
 expect(nt_rdpdr_gate_front(&p, &st, buf, len, &out), 1, "unmatched completion");
 expect_u64(out, 24, "unmatched completion length");
}

static void test_drain_ring(void) {
 NtRdpdrPolicy p = pol(0, 1, 1 << 20, 1ull << 30);
 NtRdpdrState st;
 memset(&st, 0, sizeof(st));
 unsigned char buf[64];
 for (int i = 0; i <= NT_RDPDR_EVENT_CAP; i++) {
  io_request(buf, 5, (uint32_t)i + 1, NT_IRP_MJ_WRITE, 0, 16, 0);
  expect(nt_rdpdr_gate_back(&p, &st, buf, 36), 1, "write passes");
 }
 NtRdpdrEvent ev[256];
 size_t total = 0, got;
 for (;;) {
  got = nt_rdpdr_drain(&st, ev, 256);
  if (!got) break;
  expect_u64(got, 256, "drain chunk");
  total += got;
 }
 expect_u64(total, NT_RDPDR_EVENT_CAP, "ring capped at capacity");
}

int main(void) {
 test_device_filter();
 test_back();
 test_completions();
 test_drain_ring();
 if (failures) {
  fprintf(stderr, "%d failure(s)\n", failures);
  return 1;
 }
 puts("rdpdr policy: all tests passed");
 return 0;
}
