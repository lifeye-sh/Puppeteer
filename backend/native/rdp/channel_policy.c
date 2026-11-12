#include "channel_policy.h"

#include <string.h>

/* MS-RDPECLIP wire message types as in FreeRDP's CliprdrMsgType
 * (include/freerdp/channels/cliprdr.h, CLIPRDR_HEADER.msgType). */
#define CB_MONITOR_READY 0x0001
#define CB_FORMAT_LIST 0x0002
#define CB_FORMAT_LIST_RESPONSE 0x0003
#define CB_FORMAT_DATA_REQUEST 0x0004
#define CB_FORMAT_DATA_RESPONSE 0x0005
#define CB_CLIP_CAPS 0x0007

int nt_static_channel_allowed(const char *name, const NtClipboardPolicy *p) {
 if (!p || !(p->copy || p->paste) || !name) return 0;
 return strcmp(name, NT_CLIPRDR_NAME) == 0;
}

/* Capability negotiation and the monitor-ready handshake are exchanged at
 * channel start in both directions and carry no clipboard content. Format
 * list acknowledgements are likewise direction-neutral: a response can only
 * name formats announced by a list this same filter already accepted.
 *
 * Announcements (FORMAT_LIST) and data flow are gated by direction:
 * - from the user: announcements and data answers establish content in the
 *   target (paste); data requests read target content (copy).
 * - from the target: the mirrored roles apply.
 * Everything else — temp directory, file contents, lock clipdata and any
 * unknown type — is dropped so the channel is limited to the text subset. */
static int type_allow(int from_user, int copy, int paste, unsigned int type) {
 if (type == CB_CLIP_CAPS || type == CB_MONITOR_READY || type == CB_FORMAT_LIST_RESPONSE)
  return 1;
 if (from_user)
  switch (type) {
   case CB_FORMAT_LIST:
   case CB_FORMAT_DATA_RESPONSE:
    return paste;
   case CB_FORMAT_DATA_REQUEST:
    return copy;
   default:
    return 0;
  }
 switch (type) {
  case CB_FORMAT_LIST:
  case CB_FORMAT_DATA_RESPONSE:
   return copy;
  case CB_FORMAT_DATA_REQUEST:
   return paste;
  default:
   return 0;
 }
}

int nt_cliprdr_filter(int from_user, const NtClipboardPolicy *p, const unsigned char *pdu,
                      size_t len) {
 if (!p || !pdu || len < 2) return 0;
 const unsigned int type = (unsigned int)pdu[0] | ((unsigned int)pdu[1] << 8);
 return type_allow(from_user, p->copy, p->paste, type);
}

/* Channel data flags (winpr WTS constants, duplicated to stay SDK-free). */
#define NT_FLAG_FIRST 0x1
#define NT_FLAG_LAST 0x2

void nt_cliprdr_fragment_reset(NtCliprdrFragmentState *st) {
 if (st) { st->open = 0; st->deny = 0; }
}

/* Stateful wrapper for the split transport between user and target: static
 * channel PDUs arrive in fragments flagged with the winpr channel-data bits
 * (bit 0x1 = first, bit 0x2 = last; both set on a single-fragment PDU). Only
 * the first fragment carries the message type, so a refused PDU must also
 * refuse its continuation fragments, and a fragment that starts no PDU and
 * belongs to none — which cannot be classified — is dropped. One state
 * instance per direction; each direction is fed by a single transport thread. */
int nt_cliprdr_fragment(NtCliprdrFragmentState *st, int from_user, const NtClipboardPolicy *p,
                        const unsigned char *pdu, size_t len, unsigned int flags) {
 if (!st) return 0;
 const unsigned int first = flags & NT_FLAG_FIRST, last = flags & NT_FLAG_LAST;
 if (first) {
  const int allowed = nt_cliprdr_filter(from_user, p, pdu, len);
  st->open = !last;
  st->deny = !allowed;
  return allowed;
 }
 if (last) {
  const int allowed = st->open && !st->deny;
  st->open = 0;
  return allowed;
 }
 /* neither flag: a middle fragment, allowed only inside an accepted PDU */
 return st->open && !st->deny;
}
