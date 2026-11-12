/* Standalone unit test for the channel policy. Built with plain gcc; no
 * FreeRDP SDK involved. Compiled and executed during the
 * nt-native-rdp-worker image build; can also run directly:
 * cc -o policy_test channel_policy.c channel_policy_test.c && ./policy_test */
#include "channel_policy.h"

#include <stdio.h>
#include <string.h>

static int failures;
static void expect(int got, int want, const char *what) {
 if (got != want) {
  fprintf(stderr, "FAIL %s: got %d want %d\n", what, got, want);
  failures++;
 }
}
static void expect_name(int want, const char *name, int copy, int paste) {
 NtClipboardPolicy p = {copy, paste};
 expect(nt_static_channel_allowed(name, &p), want, name ? name : "(null)");
}
#define NT_FLAG_FIRST 0x1
#define NT_FLAG_LAST 0x2
static void expect_type(int want, int from_user, int copy, int paste, unsigned low, unsigned high) {
 NtClipboardPolicy p = {copy, paste};
 unsigned char pdu[2] = {(unsigned char)low, (unsigned char)high};
 char label[64];
 snprintf(label, sizeof(label), "pdu f=%d c=%d p=%d type=%02x%02x", from_user, copy, paste, high,
          low);
 expect(nt_cliprdr_filter(from_user, &p, pdu, 2), want, label);
}
static void expect_type_le(int want, int from_user, int copy, int paste, unsigned type) {
 expect_type(want, from_user, copy, paste, type & 0xff, (type >> 8) & 0xff);
}
static void expect_frag(int want, NtCliprdrFragmentState *st, int from_user, int copy, int paste,
                        unsigned type, unsigned flags) {
 NtClipboardPolicy p = {copy, paste};
 unsigned char pdu[2] = {(unsigned char)(type & 0xff), (unsigned char)((type >> 8) & 0xff)};
 char label[80];
 snprintf(label, sizeof(label), "frag f=%d c=%d p=%d flags=%u type=%04x", from_user, copy, paste,
          flags, type);
 expect(nt_cliprdr_fragment(st, from_user, &p, pdu, 2, flags), want, label);
}

int main(void) {
 /* static channel gate: cliprdr only, exact name, active policy */
 expect_name(1, "cliprdr", 1, 0);
 expect_name(1, "cliprdr", 0, 1);
 expect_name(0, "cliprdr", 0, 0);
 expect_name(0, "cliprdrX", 1, 1);
 expect_name(0, "cliprd", 1, 1);
 expect_name(0, "CLIPRDR", 1, 1);
 expect_name(0, "Cliprdr", 1, 1);
 expect_name(0, "", 1, 1);
 expect_name(0, NULL, 1, 1);
 /* embedded NUL must not grant the channel */
 {
  char sneaky[8] = "cliprdr";
  sneaky[7] = 'x';
  expect_name(0, sneaky, 1, 1);
 }

 /* handshake subset: monitor ready, clip caps and list responses pass both ways */
 expect_type_le(1, 1, 1, 1, 0x0001);
 expect_type_le(1, 0, 1, 1, 0x0001);
 expect_type_le(1, 1, 1, 1, 0x0007);
 expect_type_le(1, 0, 1, 1, 0x0007);
 expect_type_le(1, 1, 1, 1, 0x0003);
 expect_type_le(1, 0, 1, 1, 0x0003);

 /* user announcements and data answers follow the paste policy only */
 expect_type_le(1, 1, 0, 1, 0x0002);
 expect_type_le(0, 1, 1, 0, 0x0002);
 expect_type_le(0, 1, 0, 0, 0x0002);
 expect_type_le(1, 1, 0, 1, 0x0005);
 expect_type_le(0, 1, 1, 0, 0x0005);
 /* user reads of target content follow the copy policy only */
 expect_type_le(1, 1, 1, 0, 0x0004);
 expect_type_le(0, 1, 0, 1, 0x0004);

 /* mirrored rules for target-originated PDUs */
 expect_type_le(1, 0, 1, 0, 0x0002);
 expect_type_le(0, 0, 0, 1, 0x0002);
 expect_type_le(0, 0, 0, 0, 0x0002);
 expect_type_le(1, 0, 1, 0, 0x0005);
 expect_type_le(0, 0, 0, 1, 0x0005);
 expect_type_le(1, 0, 0, 1, 0x0004);
 expect_type_le(0, 0, 1, 0, 0x0004);

 /* the full round trip must survive: copy target→user */
 expect_type_le(1, 0, 1, 0, 0x0002); /* target announces */
 expect_type_le(1, 1, 1, 0, 0x0004); /* user requests */
 expect_type_le(1, 0, 1, 0, 0x0005); /* target answers */
 /* paste user→target */
 expect_type_le(1, 1, 0, 1, 0x0002); /* user announces */
 expect_type_le(1, 0, 0, 1, 0x0004); /* target requests */
 expect_type_le(1, 1, 0, 1, 0x0005); /* user answers */

 /* non-text and unknown types are dropped in any direction or policy */
 for (unsigned t = 0x0008; t <= 0x000B; t++) expect_type_le(0, 1, 1, 1, t);
 for (unsigned t = 0x0008; t <= 0x000B; t++) expect_type_le(0, 0, 1, 1, t);
 for (unsigned t = 0x0010; t <= 0x0012; t++) expect_type_le(0, 1, 1, 1, t);
 expect_type_le(0, 1, 1, 1, 0x0000);
 expect_type_le(0, 1, 1, 1, 0x0006); /* temp directory */
 expect_type(0, 1, 1, 1, 0x00, 0x01); /* byte-swapped unknown type */
 expect_type(0, 1, 1, 1, 0x08, 0x00); /* file contents request */

 /* malformed input */
 {
  NtClipboardPolicy p = {1, 1};
  unsigned char pdu[2] = {0x02, 0x00};
  expect(nt_cliprdr_filter(1, &p, NULL, 2), 0, "null pdu");
  expect(nt_cliprdr_filter(1, &p, pdu, 1), 0, "short pdu");
  expect(nt_cliprdr_filter(1, NULL, pdu, 2), 0, "null policy");
 }

 /* fragment scenarios (winpr channel flags, 1=first, 2=last) */
 {
  NtCliprdrFragmentState st = {0, 0};
  NtClipboardPolicy p = {1, 1};
  /* refused multi-fragment PDU: first dropped, continuations stay dropped */
  expect_frag(0, &st, 1, 1, 1, 0x0008, NT_FLAG_FIRST);
  expect_frag(0, &st, 1, 1, 1, 0x0008, 0); /* middle fragment, unclassifiable */
  expect_frag(0, &st, 1, 1, 1, 0x0008, NT_FLAG_LAST); /* tail stays dropped */
  /* allowed multi-fragment PDU passes as a whole, then closes */
  expect_frag(1, &st, 1, 1, 1, 0x0002, NT_FLAG_FIRST);
  expect_frag(1, &st, 1, 1, 1, 0x0002, 0);
  expect_frag(1, &st, 1, 1, 1, 0x0002, NT_FLAG_LAST);
  /* a continuation with no PDU in progress cannot be classified */
  expect_frag(0, &st, 1, 1, 1, 0x0002, NT_FLAG_LAST);
  expect_frag(0, &st, 1, 1, 1, 0x0002, 0);
  /* unfragmented PDU (both flags) */
  expect_frag(1, &st, 1, 1, 1, 0x0004, NT_FLAG_FIRST | NT_FLAG_LAST);
  expect(nt_cliprdr_fragment(NULL, 1, &p, NULL, 2, NT_FLAG_FIRST), 0, "null state");
 }
 /* direction states are independent: reset only clears the given instance */
 {
  NtCliprdrFragmentState a = {0, 0}, b = {0, 0};
  expect_frag(1, &a, 1, 1, 1, 0x0002, NT_FLAG_FIRST);
  nt_cliprdr_fragment_reset(&a);
  expect_frag(0, &a, 1, 1, 1, 0x0002, NT_FLAG_LAST); /* stream closed again */
  expect_frag(1, &b, 0, 1, 1, 0x0002, NT_FLAG_FIRST); /* other direction intact */
 }

 if (failures) {
  fprintf(stderr, "%d failure(s)\n", failures);
  return 1;
 }
 puts("channel policy: all tests passed");
 return 0;
}
