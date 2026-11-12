/* Headless protocol test peer. Ticket arrives on stdin; target credentials are
 * never supplied. The proxy TLS certificate is pinned to a fixture PEM file. */
#define _GNU_SOURCE
#include <freerdp/freerdp.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/settings.h>
#include <winpr/wlog.h>
#include <winpr/sysinfo.h>
#include <openssl/pem.h>
#include <openssl/x509.h>
#include <openssl/crypto.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
static unsigned char expected[32];
static BOOL red, blue;
static BOOL check_paint(rdpContext *ctx){
 rdpGdi *g=ctx->gdi;
 if(!g||!g->primary_buffer||g->width<9||g->height<9)return FALSE;
 BYTE *pixel=g->primary_buffer+8*g->stride+8*4;
 if(pixel[2]>200 && pixel[0]<30)red=TRUE;
 if(red && pixel[0]>200 && pixel[2]<30)blue=TRUE;
 return TRUE;
}
static BOOL init_paint(freerdp *client){
 if(!gdi_init(client,PIXEL_FORMAT_BGRA32))return FALSE;
 client->context->update->EndPaint=check_paint;
 return TRUE;
}
static int verify(freerdp *i,const BYTE *pem,size_t n,const char *h,UINT16 p,DWORD f){
 (void)i;(void)h;(void)p;(void)f;BIO *b=BIO_new_mem_buf(pem,(int)n);X509 *c=b?PEM_read_bio_X509(b,NULL,NULL,NULL):NULL;
 unsigned char digest[EVP_MAX_MD_SIZE];unsigned int size=0;
 int ok=c&&X509_digest(c,EVP_sha256(),digest,&size)&&size==32&&CRYPTO_memcmp(digest,expected,32)==0;
 X509_free(c);BIO_free(b);return ok?1:-1;
}
int main(int argc,char **argv){
 BOOL check=argc==5 && !strcmp(argv[4],"--check-pixels");
 BOOL watch=argc==5 && !strcmp(argv[4],"--watch");
 if(argc!=4 && !check && !watch)return 2;
 signal(SIGPIPE,SIG_IGN);WLog_SetLogLevel(WLog_GetRoot(),WLOG_OFF);
 FILE *file=fopen(argv[3],"r");if(!file)return 3;X509 *cert=PEM_read_X509(file,NULL,NULL,NULL);fclose(file);
 unsigned int n=0;if(!cert||!X509_digest(cert,EVP_sha256(),expected,&n)||n!=32){X509_free(cert);return 3;}X509_free(cert);
 char ticket[256];if(!fgets(ticket,sizeof(ticket),stdin))return 4;ticket[strcspn(ticket,"\r\n")]=0;
 freerdp *client=freerdp_new();if(!client||!freerdp_context_new(client))return 5;
 client->VerifyX509Certificate=verify;rdpSettings *s=client->context->settings;
 if(check||watch)client->PostConnect=init_paint;
 if(!freerdp_settings_set_bool(s,FreeRDP_ExternalCertificateManagement,TRUE)||!freerdp_settings_set_string(s,FreeRDP_ServerHostname,argv[1])||!freerdp_settings_set_uint32(s,FreeRDP_ServerPort,(UINT32)atoi(argv[2]))||
    !freerdp_settings_set_string(s,FreeRDP_Username,ticket)||!freerdp_settings_set_string(s,FreeRDP_Password,"")||
    !freerdp_settings_set_bool(s,FreeRDP_NlaSecurity,FALSE)||!freerdp_settings_set_bool(s,FreeRDP_TlsSecurity,TRUE)||
    !freerdp_settings_set_bool(s,FreeRDP_RdpSecurity,FALSE)||!freerdp_settings_set_bool(s,FreeRDP_AutoLogonEnabled,TRUE)||
    !freerdp_settings_set_uint32(s,FreeRDP_DesktopWidth,800)||!freerdp_settings_set_uint32(s,FreeRDP_DesktopHeight,600))return 5;
 OPENSSL_cleanse(ticket,sizeof(ticket));
 BOOL connected=freerdp_connect(client);
 if(connected){
  ULONGLONG deadline=GetTickCount64()+15000;
  while(GetTickCount64()<deadline&&!freerdp_shall_disconnect_context(client->context)){
   HANDLE handles[64];DWORD count=freerdp_get_event_handles(client->context,handles,64);
   if(!count)break;
   WaitForMultipleObjects(count,handles,FALSE,50);
   if(!freerdp_check_event_handles(client->context))break;
   if(check && blue){usleep(400000);break;}
  }
 }
 BOOL disconnected=freerdp_disconnect(client);(void)disconnected;
 if((check||watch) && client->context->gdi)gdi_free(client);
 freerdp_context_free(client);freerdp_free(client);return check && !blue?7:0;
}
