/* One bounded 10 Hz recorder per single-session RDP worker. */
#ifndef NT_RDP_CAPTURE_H
#define NT_RDP_CAPTURE_H
#include <freerdp/gdi/gdi.h>
#include <stdbool.h>
bool nt_capture_start(int socket_fd); /* takes ownership */
bool nt_capture_publish(const rdpGdi *gdi);
bool nt_capture_failed(void);
bool nt_capture_stop(bool orderly);
const char *nt_capture_result(void);
#endif
