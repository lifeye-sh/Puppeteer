/* Local test target. One accepted socket on FD 3, an isolated test SAM and
 * NLA-only authentication. Emits two known bitmap updates after activation. */
#define _GNU_SOURCE
#include <freerdp/freerdp.h>
#include <freerdp/peer.h>
#include <freerdp/settings.h>
#include <freerdp/crypto/certificate.h>
#include <freerdp/crypto/privatekey.h>
#include <freerdp/channels/channels.h>
#include <freerdp/channels/wtsvc.h>
#include <winpr/ssl.h>
#include <winpr/sysinfo.h>
#include <winpr/wlog.h>
#include <signal.h>
#include <stdio.h>
#include <stdbool.h>
#include <string.h>

static bool authenticated;
static ULONGLONG activated;
static BOOL logon(freerdp_peer *peer, const SEC_WINNT_AUTH_IDENTITY *identity, BOOL automatic) {
    (void)identity;
    authenticated = automatic && freerdp_settings_get_bool(peer->context->settings,FreeRDP_NlaSecurity) &&
                    freerdp_settings_get_bool(peer->context->settings,FreeRDP_Authentication);
    return authenticated;
}
static BOOL activate(freerdp_peer *peer) {
    (void)peer;
    activated = GetTickCount64();
    return authenticated;
}
static BOOL post_connect(freerdp_peer *peer) {
    return peer->authenticated && authenticated;
}
static BOOL paint(freerdp_peer *peer, bool blue) {
    BYTE pixels[32 * 32 * 3] = {0};
    for (size_t i = 0; i < sizeof(pixels); i += 3) pixels[i + (blue ? 0 : 2)] = 240;
    BITMAP_DATA rect = { .destRight=31, .destBottom=31, .width=32, .height=32,
        .bitsPerPixel=24, .bitmapLength=sizeof(pixels), .bitmapDataStream=pixels };
    BITMAP_UPDATE bitmap = { .number=1, .rectangles=&rect, .skipCompression=TRUE };
    return peer->context->update->BitmapUpdate(peer->context, &bitmap);
}

int main(int argc, char **argv) {
    if (argc != 4) return 2;
    signal(SIGPIPE,SIG_IGN);
    WLog_SetLogLevel(WLog_GetRoot(), WLOG_OFF);
    WSADATA wsa;
    if (!WTSRegisterWtsApiFunctionTable(FreeRDP_InitWtsApi()) ||
        !winpr_InitializeSSL(WINPR_SSL_INIT_DEFAULT) || WSAStartup(MAKEWORD(2,2), &wsa)) return 3;
    freerdp_peer *p=freerdp_peer_new(3);
    if (!p || !freerdp_peer_context_new(p)) return 4;
    rdpSettings *s=p->context->settings;
    rdpPrivateKey *key=freerdp_key_new_from_file_enc(argv[2],NULL);
    rdpCertificate *cert=freerdp_certificate_new_from_file(argv[1]);
    if (!cert || !key ||
        !freerdp_settings_set_pointer_len(s,FreeRDP_RdpServerRsaKey,key,1) ||
        !freerdp_settings_set_pointer_len(s,FreeRDP_RdpServerCertificate,cert,1) ||
        !freerdp_settings_set_string(s,FreeRDP_NtlmSamFile,argv[3]) ||
        !freerdp_settings_set_bool(s,FreeRDP_Authentication,TRUE) ||
        !freerdp_settings_set_bool(s,FreeRDP_NlaSecurity,TRUE) ||
        !freerdp_settings_set_bool(s,FreeRDP_TlsSecurity,FALSE) ||
        !freerdp_settings_set_bool(s,FreeRDP_RdpSecurity,FALSE) ||
        !freerdp_settings_set_bool(s,FreeRDP_ExtSecurity,FALSE)) return 5;
    p->Logon=logon;
    p->PostConnect=post_connect;
    p->Activate=activate;
    if (!p->Initialize(p)) return 6;
    int sent=0;
    ULONGLONG deadline=GetTickCount64()+22000;
    while (GetTickCount64()<deadline) {
        HANDLE handles[64];
        DWORD count=p->GetEventHandles(p,handles,64);
        if (!count || WaitForMultipleObjects(count,handles,FALSE,20)==WAIT_FAILED ||
            !p->CheckFileDescriptor(p)) break;
        if (activated && sent<2 && GetTickCount64()-activated >= (unsigned)(400+sent*700)) {
            if (!paint(p,sent!=0)) break;
            ++sent;
        }
    }
    DWORD error=freerdp_get_last_error(p->context);
    p->Disconnect(p);
    freerdp_peer_context_free(p);
    freerdp_peer_free(p);
    printf("authenticated=%d colors=%d error=%u\n",authenticated,sent,error);
    return authenticated && GetTickCount64()<deadline ? 0 : 7;
}
