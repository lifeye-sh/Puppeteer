/* Native RDP channel policy: static-channel gate and MS-RDPECLIP direction
 * filter. Pure C with no FreeRDP dependency so it can be unit-tested without
 * the SDK. The clipboard policy is delivered by the Go supervisor in the
 * target payload; the worker never derives it from client-provided data.
 *
 * copy  = transfer clipboard content from the RDP target to the user.
 * paste = transfer clipboard content from the user to the RDP target.
 */
#ifndef NT_CHANNEL_POLICY_H
#define NT_CHANNEL_POLICY_H
#include <stddef.h>

#define NT_CLIPRDR_NAME "cliprdr"

typedef struct {
 int copy;
 int paste;
} NtClipboardPolicy;

/* Gate for the back-end connect channel list (FILTER_TYPE_CLIENT_PASSTHROUGH
 * CHANNEL_CREATE). Returns 1 only for NT_CLIPRDR_NAME and only when some
 * clipboard direction is enabled. Every other static channel — including
 * unknown or malformed names — is refused, so the target never receives it. */
int nt_static_channel_allowed(const char *name, const NtClipboardPolicy *p);

/* MS-RDPECLIP message-level filter for the cliprdr static channel.
 * from_user selects the direction: front→back (user side) when non-zero,
 * back→front (target side) when zero. pdu points at the first bytes of the
 * channel PDU; the 2-byte little-endian message type is the only field read.
 *
 * Returns 1 when the PDU may pass, 0 when it must be dropped. Truncated PDUs,
 * file-object and other non-text message types (MS-RDPECLIP 2.2.2) and types
 * outside the negotiated directions are all dropped. */
int nt_cliprdr_filter(int from_user, const NtClipboardPolicy *p, const unsigned char *pdu,
                      size_t len);

/* Fragment-aware wrapper. Channel PDU may arrive split across several channel
 * fragments carrying the winpr channel-data flags (bit 0x1 = first fragment,
 * bit 0x2 = last fragment; zero stands for an unfragmented PDU). Use one
 * NtCliprdrFragmentState per direction. Continuations of a refused PDU are
 * refused and unclassifiable fragments are dropped, keeping the peer stream
 * consistent. */
typedef struct {
 int open; /* a multi-fragment PDU is currently in progress */
 int deny; /* the PDU in progress was refused */
} NtCliprdrFragmentState;
void nt_cliprdr_fragment_reset(NtCliprdrFragmentState *st);
int nt_cliprdr_fragment(NtCliprdrFragmentState *st, int from_user, const NtClipboardPolicy *p,
                        const unsigned char *pdu, size_t len, unsigned int flags);

#endif
