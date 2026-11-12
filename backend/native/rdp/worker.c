/* Single-session authentication probe or mandatory recorded view-only desktop.
 * Both profiles require the platform ticket gate and pinned upstream NLA.
 * FD3=accepted TCP, FD4=32-byte nonce, FD5=READY pipe (Go supervisor).
 */
#define _GNU_SOURCE
#include "capture.h"
#include "channel_policy.h"
#include "rdpdr_policy.h"
#include <freerdp/freerdp.h>
#include <freerdp/settings.h>
#include <freerdp/channels/wtsvc.h>
#include <freerdp/channels/channels.h>
#include <winpr/ssl.h>
#include <freerdp/server/proxy/proxy_server.h>
#include <freerdp/server/proxy/proxy_context.h>
#include <winpr/wlog.h>
#include <json-c/json.h>
#include <openssl/pem.h>
#include <openssl/x509.h>
#include <openssl/crypto.h>
#include <pthread.h>
#include <stdatomic.h>
#include <signal.h>
#include <sys/socket.h>
#include <arpa/inet.h>
#include <stdbool.h>
#include <sys/un.h>
#include <sys/stat.h>
#include <poll.h>
#include <unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct {
 int control;
 uint64_t sequence;
 pthread_mutex_t rpc_lock;
 atomic_bool consumed, prepared, started, done;
 atomic_llong lease;
 char session[37];
 char path[101];
 char recording_path[101];
 char server_name[256];
 unsigned char pin[32];
 BOOL readonly;
 int clipboard_copy, clipboard_paste;
 NtCliprdrFragmentState front_frag, back_frag;
 // Client-drive profile: policy and counters are worker-owned; the SDK patch
 // only consults the bridge below.
 NtRdpdrPolicy drive_pol;
 NtRdpdrState drive_state;
 int64_t drive_last_flush;
 pthread_mutex_t drive_lock;
} Worker;
static Worker worker = {.control=-1, .rpc_lock=PTHREAD_MUTEX_INITIALIZER, .drive_lock=PTHREAD_MUTEX_INITIALIZER};
static volatile sig_atomic_t interrupted;
static bool record_view;
static atomic_bool session_ended;
// Development probe diagnostics contain fixed stage names, never RDP fields.
static int trace_fd=-1;
static void trace(const char *stage){if(trace_fd>=0)dprintf(trace_fd,"%s\n",stage);}

static int64_t clock_ns(clockid_t id) {
 struct timespec ts; if (clock_gettime(id,&ts)) return 0;
 return (int64_t)ts.tv_sec*1000000000LL+ts.tv_nsec;
}
static void stop_signal(int sig) {(void)sig; interrupted=1;}
static int exact(int fd, void *buf, size_t size, int writing) {
 unsigned char *p=buf;
 while(size){ssize_t n=writing?write(fd,p,size):read(fd,p,size);
  if(n<0&&errno==EINTR)continue;
  if(n<=0)return 0;
  p+=n;size-=(size_t)n;}
 return 1;
}
static void destroy_json(json_object *o) {
 if(!o)return;
 if(json_object_is_type(o,json_type_object)){
  json_object_object_foreach(o,key,value){(void)key;
   if(json_object_is_type(value,json_type_string))
    OPENSSL_cleanse((void*)json_object_get_string(value),(size_t)json_object_get_string_len(value));
  }
 }
 json_object_put(o);
}
static const char *field(json_object *o,const char *name,size_t max){
 json_object *v=NULL;
 if(!json_object_object_get_ex(o,name,&v)||!json_object_is_type(v,json_type_string))return NULL;
 const char *s=json_object_get_string(v);size_t n=(size_t)json_object_get_string_len(v);
 return n<=max && strlen(s)==n?s:NULL;
}
static int uuid(const char *s) {
 if(!s||strlen(s)!=36)return 0;
 for(size_t i=0;i<36;i++){
  if(i==8||i==13||i==18||i==23){if(s[i]!='-')return 0;}
  else if(!((s[i]>='0'&&s[i]<='9')||(s[i]>='a'&&s[i]<='f')))return 0;
 }return 1;
}
static int ticket_valid(const char *s){
 if(!s||strlen(s)!=109||memcmp(s,"NTICKET:",8)||s[44]!=':')return 0;
 char id[37];memcpy(id,s+8,36);id[36]=0;if(!uuid(id))return 0;
 for(size_t i=45;i<109;i++)if(!((s[i]>='0'&&s[i]<='9')||(s[i]>='a'&&s[i]<='f')))return 0;
 return 1;
}
static int update_lease(json_object *reply) {
 const char *s=field(reply,"leaseUntil",40);if(!s)return 0;
 struct tm tm={0};char *p=strptime(s,"%Y-%m-%dT%H:%M:%S",&tm);if(!p)return 0;
 int64_t fraction=0;int digits=0;
 if(*p=='.'){p++;while(*p>='0'&&*p<='9'&&digits<9){fraction=fraction*10+(*p++-'0');digits++;}if(!digits)return 0;}
 if(strcmp(p,"Z"))return 0;
 while(digits++<9)fraction*=10;
 int64_t delta=(int64_t)timegm(&tm)*1000000000LL+fraction-clock_ns(CLOCK_REALTIME);
 if(delta<=0||delta>15000000000LL)return 0;
 atomic_store(&worker.lease,clock_ns(CLOCK_MONOTONIC)+delta);return 1;
}
static json_object *rpc(const char *method,const char *ticket,json_object *payload) {
 json_object *reply=NULL,*req=NULL;unsigned char *raw=NULL;size_t size=0;
 pthread_mutex_lock(&worker.rpc_lock);
 if(worker.control<0)goto out;
 uint64_t id=++worker.sequence;
 req=json_object_new_object();json_object_object_add(req,"id",json_object_new_uint64(id));
 json_object_object_add(req,"method",json_object_new_string(method));
 if(ticket)json_object_object_add(req,"ticket",json_object_new_string(ticket));
 if(payload)json_object_object_add(req,"payload",payload);
 const char *wire=json_object_to_json_string_ext(req,JSON_C_TO_STRING_PLAIN);size_t n=strlen(wire);
 uint32_t header=htonl((uint32_t)n);
 int sent=exact(worker.control,&header,4,1)&&exact(worker.control,(void*)wire,n,1);
 OPENSSL_cleanse((void*)wire,n);destroy_json(req);req=NULL;
 if(!sent||!exact(worker.control,&header,4,0))goto fail;
 size=ntohl(header);if(!size||size>65536)goto fail;
 raw=malloc(size);if(!raw||!exact(worker.control,raw,size,0))goto fail;
 json_tokener *tok=json_tokener_new();if(!tok)goto fail;
 json_tokener_set_flags(tok,JSON_TOKENER_STRICT|JSON_TOKENER_VALIDATE_UTF8);
 reply=json_tokener_parse_ex(tok,(const char*)raw,(int)size);
 int valid=json_tokener_get_error(tok)==json_tokener_success&&json_tokener_get_parse_end(tok)==size;
 json_tokener_free(tok);
 json_object *rid=NULL;
 if(!valid||!reply||!json_object_is_type(reply,json_type_object)||
    !json_object_object_get_ex(reply,"id",&rid)||!json_object_is_type(rid,json_type_int)||json_object_get_uint64(rid)!=id)goto fail;
 goto out;
fail:
 destroy_json(reply);reply=NULL;atomic_store(&worker.done,true);
out:
 if(raw){OPENSSL_cleanse(raw,size);free(raw);}destroy_json(req);
 pthread_mutex_unlock(&worker.rpc_lock);return reply;
}

static BOOL transport_logon(proxyPlugin *p,proxyData *data,void *arg) {
 trace("tls-logon");
 (void)p;proxyServerPeerLogon *logon=arg;
 // TLS Logon occurs before ClientInfo. This only accepts the TLS transport;
 // it MUST NOT consume or authorize a ticket. FetchTarget below is the gate.
 return logon&&!logon->automatic&&data&&data->ps&&
  freerdp_settings_get_bool(data->ps->settings,FreeRDP_TlsSecurity)&&
  !freerdp_settings_get_bool(data->ps->settings,FreeRDP_NlaSecurity);
}
static BOOL fetch_target(proxyPlugin *p,proxyData *data,void *arg) {
 trace("fetch-target");
 (void)p;proxyFetchTargetEventInfo *event=arg;
 if(!data||!data->ps||!data->pc||!event||atomic_load(&worker.consumed))return FALSE;
 rdpSettings *front=data->ps->settings;
 const char *ticket=freerdp_settings_get_string(front,FreeRDP_Username);
 if(!ticket_valid(ticket)||!freerdp_settings_get_bool(front,FreeRDP_TlsSecurity))return FALSE;
 json_object *reply=rpc("ConsumeTicket",ticket,NULL);
 const char *id=reply?field(reply,"sessionId",36):NULL;
 if(!uuid(id)||memcmp(id,ticket+8,36)||!update_lease(reply)){destroy_json(reply);return FALSE;}
 memcpy(worker.session,id,37);destroy_json(reply);atomic_store(&worker.consumed,true);
 trace("ticket-consumed");
 // Remove the ingress ticket so it cannot be used as upstream credentials.
 if(!freerdp_settings_set_string(front,FreeRDP_Username,NULL))return FALSE;
 reply=rpc("PrepareTarget",NULL,NULL);json_object *target=NULL,*flag=NULL;
 if(!reply||!json_object_object_get_ex(reply,"payload",&target)||!json_object_is_type(target,json_type_object))goto fail;
 const char *path=field(target,"socketPath",100),*host=field(target,"serverName",255);
 const char *pin=field(target,"certificateFingerprint",71),*user=field(target,"username",512);
 const char *password=field(target,"password",16384),*security=field(target,"security",8);
 if(!path||path[0]!='/'||!host||!*host||!pin||strlen(pin)!=71||strncmp(pin,"sha256:",7)||!user||!*user||!password||!security||strcmp(security,"nla"))goto fail;
 for(int i=0;i<32;i++){
  unsigned int v;char hex[3]={pin[7+i*2],pin[8+i*2],0};
  if(!strchr("0123456789abcdef",hex[0])||!strchr("0123456789abcdef",hex[1])||sscanf(hex,"%2x",&v)!=1)goto fail;
  worker.pin[i]=(unsigned char)v;
 }
 const char *disabled[]={"audio"};
 for(size_t i=0;i<1;i++)if(!json_object_object_get_ex(target,disabled[i],&flag)||!json_object_is_type(flag,json_type_boolean)||json_object_get_boolean(flag))goto fail;
 if(!json_object_object_get_ex(target,"readonly",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 worker.readonly=json_object_get_boolean(flag);
 // Clipboard direction policy is platform data. Paste is impossible on a
 // readonly session; refuse any payload that disagrees with the session mode
 // instead of silently narrowing the policy.
 if(!json_object_object_get_ex(target,"clipboardCopy",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 worker.clipboard_copy=json_object_get_boolean(flag);
 if(!json_object_object_get_ex(target,"clipboardPaste",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 worker.clipboard_paste=json_object_get_boolean(flag);
 if(worker.readonly&&worker.clipboard_paste)goto fail;
 // Client-drive profile: enabled only from the platform with sane limits.
 // A disabled profile keeps zero policy, so every announcement and IO request
 // is refused by the gates.
 memset(&worker.drive_pol,0,sizeof(worker.drive_pol));memset(&worker.drive_state,0,sizeof(worker.drive_state));
 worker.drive_last_flush=0;
 if(!json_object_object_get_ex(target,"drives",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 const int drives=json_object_get_boolean(flag);
 if(!json_object_object_get_ex(target,"driveUpload",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 worker.drive_pol.upload=json_object_get_boolean(flag)?1:0;
 if(!json_object_object_get_ex(target,"driveDownload",&flag)||!json_object_is_type(flag,json_type_boolean))goto fail;
 worker.drive_pol.download=json_object_get_boolean(flag)?1:0;
 if(!drives&&(worker.drive_pol.upload||worker.drive_pol.download))goto fail;
 if(drives&&(worker.drive_pol.upload||worker.drive_pol.download)){
  const char *limits[]={"driveMaxReadOp","driveMaxWriteOp"};
  for(size_t i=0;i<2;i++){
   if(!json_object_object_get_ex(target,limits[i],&flag)||!json_object_is_type(flag,json_type_int))goto fail;
   const uint64_t v=json_object_get_uint64(flag);
   if(!v||v>(32ull<<20))goto fail;
   (&worker.drive_pol.max_read_op)[i]=(uint32_t)v;
  }
  const char *totals[]={"driveMaxReadTotal","driveMaxWriteTotal"};
  for(size_t i=0;i<2;i++){
   if(!json_object_object_get_ex(target,totals[i],&flag)||!json_object_is_type(flag,json_type_int))goto fail;
   const uint64_t v=json_object_get_uint64(flag);
   if(!v||v>(4ull<<30))goto fail;
   (&worker.drive_pol.max_read_total)[i]=v;
  }
  if(!worker.drive_pol.max_read_op||!worker.drive_pol.max_write_op||!worker.drive_pol.max_read_total||!worker.drive_pol.max_write_total)goto fail;
 } else worker.drive_pol.upload=worker.drive_pol.download=0;
 if(record_view){
  const char *recording=field(target,"recordingSocket",100);
  if(!recording||recording[0]!='/')goto fail;
  strcpy(worker.recording_path,recording);
 }
 rdpSettings *back=data->pc->settings;
 // Do not inherit a domain, routing token, gateway, or credential from ClientInfo.
 if(!freerdp_settings_set_string(back,FreeRDP_Username,user)||!freerdp_settings_set_string(back,FreeRDP_Password,password)||
    !freerdp_settings_set_string(back,FreeRDP_Domain,"")||!freerdp_settings_set_string(back,FreeRDP_CertificateName,host)||
    !freerdp_settings_set_string(back,FreeRDP_UserSpecifiedServerName,host)||
    !freerdp_settings_set_bool(back,FreeRDP_GatewayEnabled,FALSE))goto fail;
 // Keep the bounded, platform-issued UDS path for both pre-connect checks.
 strcpy(worker.path,path);
 event->fetch_method=PROXY_FETCH_TARGET_USE_CUSTOM_ADDR;event->target_address=strdup(path);event->target_port=3389;
 if(!event->target_address)goto fail;
 trace("target-prepared");
 atomic_store(&worker.prepared,true);json_object_get(target);destroy_json(target);json_object_object_del(reply,"payload");destroy_json(reply);return TRUE;
fail:
 if(target){json_object_get(target);destroy_json(target);}destroy_json(reply);return FALSE;
}
static int verify_certificate(freerdp *instance,const BYTE *pem,size_t length,const char *host,UINT16 port,DWORD flags) {
 trace("target-certificate");
 (void)instance;(void)host;(void)port;(void)flags;
 if(!atomic_load(&worker.prepared)||!pem||length>65536)return -1;
 BIO *bio=BIO_new_mem_buf(pem,(int)length);X509 *cert=bio?PEM_read_bio_X509(bio,NULL,NULL,NULL):NULL;
 unsigned char digest[EVP_MAX_MD_SIZE];unsigned int n=0;
 int ok=cert&&X509_cmp_current_time(X509_get0_notBefore(cert))<0&&
  X509_cmp_current_time(X509_get0_notAfter(cert))>0&&
  X509_digest(cert,EVP_sha256(),digest,&n)&&n==32&&CRYPTO_memcmp(digest,worker.pin,32)==0;
 X509_free(cert);BIO_free(bio);OPENSSL_cleanse(digest,sizeof(digest));return ok?1:-1;
}
static BOOL before_connect(proxyPlugin *p,proxyData *data,void *arg) {
 trace("before-target-connect");
 (void)p;(void)arg;
 if(!atomic_load(&worker.prepared)||!atomic_load(&worker.consumed)||!data||!data->pc||atomic_load(&worker.done))return FALSE;
 rdpSettings *s=data->pc->settings;
 const char *host=freerdp_settings_get_string(s,FreeRDP_ServerHostname);
 if(!host||strcmp(host,worker.path)){trace("target-path-rejected");return FALSE;}
 if(record_view){
  UINT32 w=freerdp_settings_get_uint32(s,FreeRDP_DesktopWidth);
  UINT32 h=freerdp_settings_get_uint32(s,FreeRDP_DesktopHeight);
  if(w<2 || h<2 || w>1920 || h>1080){trace("target-size-rejected");return FALSE;}
 }
 data->pc->instance->VerifyX509Certificate=verify_certificate;
 if(record_view && (!freerdp_settings_set_bool(s,FreeRDP_DeactivateClientDecoding,FALSE) ||
    !freerdp_settings_set_bool(s,FreeRDP_SoftwareGdi,TRUE) ||
    !freerdp_settings_set_bool(s,FreeRDP_UseMultimon,FALSE))) return FALSE;
 return freerdp_settings_set_bool(s,FreeRDP_ExternalCertificateManagement,TRUE)&&
  freerdp_settings_set_bool(s,FreeRDP_Authentication,TRUE)&&
  freerdp_settings_set_bool(s,FreeRDP_DisableCredentialsDelegation,FALSE)&&
  freerdp_settings_set_pointer_len(s,FreeRDP_LoadBalanceInfo,NULL,0)&&
  freerdp_settings_set_bool(s,FreeRDP_SmartcardLogon,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_RedirectSmartCards,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_RestrictedAdminModeRequired,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_IgnoreCertificate,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_AutoAcceptCertificate,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_RedirectClipboard,FALSE)&&
  freerdp_settings_set_bool(s,FreeRDP_DeviceRedirection,FALSE);
}
static int connect_control(const char *path);
static BOOL start_desktop(proxyPlugin *p,proxyData *data,void *arg) {
 (void)p;(void)arg;
 if(record_view && data && data->pc && atomic_load(&worker.prepared) &&
    !atomic_load(&worker.done) && clock_ns(CLOCK_MONOTONIC)<atomic_load(&worker.lease)){
  rdpSettings *s=data->pc->settings;
  UINT32 w=freerdp_settings_get_uint32(s,FreeRDP_DesktopWidth);
  UINT32 h=freerdp_settings_get_uint32(s,FreeRDP_DesktopHeight);
  // The proxy copies negotiated settings to the ingress peer at reactivation.
  // The target credentials have served their purpose once PostConnect runs.
  if(w>=2 && w<=1920 && h>=2 && h<=1080 &&
     freerdp_settings_set_string(s,FreeRDP_Password,NULL) &&
     freerdp_settings_set_string(s,FreeRDP_Username,NULL) &&
     freerdp_settings_set_string(s,FreeRDP_Domain,NULL) &&
     nt_capture_start(connect_control(worker.recording_path))){
   trace("capture-ready");
   return TRUE;
  }
 }
 atomic_store(&worker.done,true);
 return FALSE;
}
static BOOL end_paint(proxyPlugin *p,proxyData *data,void *arg){
 (void)p;
 rdpContext *upstream=arg;
 if(record_view && upstream && data && data->pc==upstream && nt_capture_publish(upstream->gdi))return TRUE;
 trace("capture-paint-rejected");
 atomic_store(&worker.done,true);return FALSE;
}
static BOOL session_end(proxyPlugin *p,proxyData *data,void *arg){(void)p;(void)data;(void)arg;trace("session-end");atomic_store(&session_ended,true);atomic_store(&worker.done,true);return TRUE;}
static BOOL session_initialize(proxyPlugin *p,proxyData *data,void *arg){(void)p;(void)data;(void)arg;trace("session-initialize");return TRUE;}
static BOOL session_started(proxyPlugin *p,proxyData *data,void *arg){(void)p;(void)data;(void)arg;trace("session-started");atomic_store(&worker.started,true);return TRUE;}
static BOOL deny(proxyPlugin *p,proxyData *data,void *arg){(void)p;(void)data;(void)arg;return FALSE;}
// Audit batch for the client-drive gates: bounded per frame, flushed at most
// every 500ms; Finalize drains whatever remains. Event loss beyond the batch
// ring is recorded on the worker side, not fabricated here.
static void drive_flush(int final) {
 if(!atomic_load(&worker.consumed))return;
 pthread_mutex_lock(&worker.drive_lock);
 const int64_t now=clock_ns(CLOCK_MONOTONIC);
 const size_t queued=worker.drive_state.event_n;
 if(!final && !queued)
  {pthread_mutex_unlock(&worker.drive_lock);return;}
 if(!final && now-worker.drive_last_flush<500000000LL && queued<100)
  {pthread_mutex_unlock(&worker.drive_lock);return;}
 worker.drive_last_flush=now;
 NtRdpdrEvent events[256];
 size_t n=nt_rdpdr_drain(&worker.drive_state,events,256);
 json_object *arr=json_object_new_array();
 for(size_t i=0;i<n;i++){
  json_object *o=json_object_new_object();
  json_object_object_add(o,"op",json_object_new_int(events[i].op));
  json_object_object_add(o,"dir",json_object_new_int(events[i].dir));
  json_object_object_add(o,"device",json_object_new_uint64(events[i].device));
  json_object_object_add(o,"completion",json_object_new_uint64(events[i].completion));
  json_object_object_add(o,"bytes",json_object_new_uint64(events[i].bytes));
  json_object_object_add(o,"offset",json_object_new_uint64(events[i].offset));
  if(events[i].name[0])json_object_object_add(o,"name",json_object_new_string((char*)events[i].name));
  json_object_array_add(arr,o);
 }
 // Session totals travel with every batch; the final frame keeps its truth.
 json_object *body=json_object_new_object();
 json_object_object_add(body,"ops",arr);
 json_object_object_add(body,"readTotal",json_object_new_uint64(worker.drive_state.read_total));
 json_object_object_add(body,"writeTotal",json_object_new_uint64(worker.drive_state.write_total));
 pthread_mutex_unlock(&worker.drive_lock);
 if(n){json_object *r=rpc("ReportDeviceIO",NULL,body);destroy_json(r);}
 else json_object_put(body);
}
// Bridge hooks resolved by the patched FreeRDP proxy (weak declarations there,
// strong definitions here). A disabled profile refuses everything.
int nt_rdpdr_back_gate(const unsigned char *io,size_t len){
 if(!worker.drive_pol.upload&&!worker.drive_pol.download)return 0;
 pthread_mutex_lock(&worker.drive_lock);
 const int ok=nt_rdpdr_gate_back(&worker.drive_pol,&worker.drive_state,io,len);
 pthread_mutex_unlock(&worker.drive_lock);
 drive_flush(0);
 return ok;
}
int nt_rdpdr_front_gate(unsigned char *io,size_t len,size_t *out){
 if(!worker.drive_pol.upload&&!worker.drive_pol.download){*out=0;return 0;}
 pthread_mutex_lock(&worker.drive_lock);
 const int ok=nt_rdpdr_gate_front(&worker.drive_pol,&worker.drive_state,io,len,out);
 pthread_mutex_unlock(&worker.drive_lock);
 drive_flush(0);
 return ok;
}
/* Channel policy gate for the back-end channel announcement: only cliprdr may
 * be declared to the target, and only when the platform enabled a clipboard
 * direction. Every other static channel — including unknown names — and every
 * dynamic channel stays refused. */
static BOOL channel_create_gate(proxyPlugin *p,proxyData *data,void *arg){
 (void)p;(void)data;
 proxyChannelDataEventInfo *ch=arg;
 if(!ch||!ch->channel_name)return FALSE;
 NtClipboardPolicy pol={worker.clipboard_copy,worker.clipboard_paste};
 if(nt_static_channel_allowed(ch->channel_name,&pol))return TRUE;
 // The drive profile needs rdpdr on the target side; the announcement gate
 // plus per-operation limits live in nt_rdpdr_*. A disabled profile keeps the
 // channel refused, exactly as before the profile existed.
 return !strcmp(ch->channel_name,NT_RDPDR_NAME)&&(worker.drive_pol.upload||worker.drive_pol.download);
}
/* ServerChannelData carries front→back (user→target) PDUs; ClientChannelData
 * back→front (target→user). Each applies the MS-RDPECLIP direction filter for
 * cliprdr only; the per-direction fragment state keeps refusals and split
 * PDUs consistent. */
static BOOL channel_data_gate(int from_user,proxyPlugin *p,proxyData *data,void *arg){
 (void)p;(void)data;
 proxyChannelDataEventInfo *ev=arg;
 if(!ev||!ev->channel_name||strcmp(ev->channel_name,NT_CLIPRDR_NAME))return FALSE;
 NtClipboardPolicy pol={worker.clipboard_copy,worker.clipboard_paste};
 if(!pol.copy&&!pol.paste)return FALSE;
 return nt_cliprdr_fragment(from_user?&worker.front_frag:&worker.back_frag,from_user,
  &pol,ev->data,ev->data_len,ev->flags)!=0;
}
static BOOL server_channel_data(proxyPlugin *p,proxyData *data,void *arg){return channel_data_gate(1,p,data,arg);}
static BOOL client_channel_data(proxyPlugin *p,proxyData *data,void *arg){return channel_data_gate(0,p,data,arg);}
static BOOL module(proxyPluginsManager *manager,void *userdata){
 (void)userdata;proxyPlugin p={0};p.name="nt-auth-probe";p.description="Mandatory platform ticket gate";
 p.ServerPeerLogon=transport_logon;p.ServerFetchTargetAddr=fetch_target;
 p.ServerSessionInitialize=session_initialize;p.ServerSessionStarted=session_started;
 p.ClientInitConnect=before_connect;p.ClientPreConnect=before_connect;
 p.ClientPostConnect=start_desktop;p.ServerSessionEnd=session_end;p.ClientRedirect=deny;
 if(record_view)p.ClientEndPaint=end_paint;
 p.KeyboardEvent=deny;p.UnicodeEvent=deny;p.MouseEvent=deny;p.MouseExEvent=deny;
 p.ClientChannelData=client_channel_data;p.ServerChannelData=server_channel_data;
 p.ChannelCreate=channel_create_gate;p.DynamicChannelCreate=deny;
 return manager->RegisterPlugin(manager,&p);
}
static int connect_control(const char *path){
 struct sockaddr_un addr={.sun_family=AF_UNIX};if(strlen(path)>=sizeof(addr.sun_path))return -1;
 strcpy(addr.sun_path,path);int fd=socket(AF_UNIX,SOCK_STREAM|SOCK_CLOEXEC,0);if(fd<0)return -1;
 if(connect(fd,(struct sockaddr*)&addr,sizeof(addr))){close(fd);return -1;}
 struct ucred peer={0};socklen_t size=sizeof(peer);
 if(getsockopt(fd,SOL_SOCKET,SO_PEERCRED,&peer,&size)||peer.pid!=getppid()||peer.uid!=geteuid()){close(fd);return -1;}
 struct timeval timeout={.tv_sec=5};setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));setsockopt(fd,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
 return fd;
}
int main(int argc,char **argv){
 if(argc!=5)return 2;
 record_view=!strcmp(argv[1],"--record-view");
 if(!record_view && strcmp(argv[1],"--auth-probe"))return 2;
 for(int i=2;i<5;i++)if(argv[i][0]!='/'||strpbrk(argv[i],"\r\n"))return 2;
 char trace_path[200];int trace_size=snprintf(trace_path,sizeof(trace_path),"%s.trace",argv[2]);
 if(trace_size>0&&(size_t)trace_size<sizeof(trace_path))trace_fd=open(trace_path,O_WRONLY|O_CREAT|O_TRUNC|O_NOFOLLOW|O_CLOEXEC,0600);
 trace("starting");
 // FreeRDP settings initialization requires HOME. Never inherit the host's
 // configuration or certificate cache: use a fresh directory under control root.
 char private_home[200];int home_size=snprintf(private_home,sizeof(private_home),"%s.home.XXXXXX",argv[2]);
 if(home_size<0||(size_t)home_size>=sizeof(private_home)||!mkdtemp(private_home)||setenv("HOME",private_home,1))return 3;
 signal(SIGPIPE,SIG_IGN);signal(SIGTERM,stop_signal);signal(SIGINT,stop_signal);
 WLog_SetLogLevel(WLog_GetRoot(),WLOG_OFF);
 unsigned char nonce[32];if(!exact(4,nonce,32,0))return 3;close(4);
 worker.control=connect_control(argv[2]);if(worker.control<0){OPENSSL_cleanse(nonce,32);return 3;}
 int sent=exact(worker.control,nonce,32,1);OPENSSL_cleanse(nonce,32);if(!sent){close(worker.control);return 3;}
 char config[8192];int n=snprintf(config,sizeof(config),
  "[Server]\nHost=127.0.0.1\nPort=3390\n[Target]\nFixedTarget=false\nCertificatePolicy=deny\nTlsSecLevel=2\n"
  "[Security]\nServerTlsSecurity=true\nServerRdpSecurity=false\nServerNlaSecurity=false\nServerExtSecurity=false\nClientNlaSecurity=true\nClientTlsSecurity=false\nClientRdpSecurity=false\nClientExtSecurity=false\nClientAllowFallbackToTls=false\n"
  "[Channels]\nGFX=false\nDisplayControl=false\nClipboard=false\nAudioOutput=false\nAudioInput=false\nRemoteApp=false\nDeviceRedirection=false\nVideoRedirection=false\nCameraRedirection=false\nPassthroughIsBlacklist=false\nPassthrough=cliprdr\nIntercept=rdpdr\n"
  "[Input]\nKeyboard=false\nMouse=false\nMultitouch=false\n[Codecs]\nRFX=false\nNSC=false\n[Certificates]\nCertificateFile=%s\nPrivateKeyFile=%s\n",argv[3],argv[4]);
 if(n<0||(size_t)n>=sizeof(config))return 4;
 proxyConfig *cfg=pf_server_config_load_buffer(config);if(!cfg)return 4;
 // The accepted-socket helper does not run pf_server_start's process setup.
 WSADATA wsa;
 if(!WTSRegisterWtsApiFunctionTable(FreeRDP_InitWtsApi())||
    !winpr_InitializeSSL(WINPR_SSL_INIT_DEFAULT)||WSAStartup(MAKEWORD(2,2),&wsa))return 4;
 proxyServer *server=pf_server_new(cfg);pf_server_config_free(cfg);if(!server)return 4;
 trace("configured");
 if(!pf_server_add_module(server,module,NULL)||!pf_server_start_with_peer_socket(server,3)){pf_server_free(server);return 4;}
 trace("peer-started");
 int64_t ready_deadline=clock_ns(CLOCK_MONOTONIC)+3000000000LL;
 while(!atomic_load(&worker.started)&&!atomic_load(&worker.done)&&!interrupted&&clock_ns(CLOCK_MONOTONIC)<ready_deadline)usleep(10000);
 if(!atomic_load(&worker.started)){pf_server_stop(server);pf_server_free(server);return 4;}
 if(!exact(5,"READY\n",6,1))atomic_store(&worker.done,true);
 close(5);
 int64_t start=clock_ns(CLOCK_MONOTONIC),renewed=start;
 while(!interrupted&&!atomic_load(&worker.done)&&!nt_capture_failed()){
  int64_t now=clock_ns(CLOCK_MONOTONIC);
  if(atomic_load(&worker.consumed)){
   if(now>=atomic_load(&worker.lease))break;
   if(now-renewed>2000000000LL){json_object *r=rpc("RenewSession",NULL,NULL);int ok=r&&update_lease(r);destroy_json(r);if(!ok)break;renewed=clock_ns(CLOCK_MONOTONIC);}
  }else if(now-start>30000000000LL)break;
  struct pollfd f={.fd=worker.control,.events=0};poll(&f,1,50);if(f.revents&(POLLHUP|POLLERR|POLLNVAL))break;
 }
 if(nt_capture_failed())trace("capture-sampler-failed");
 if(interrupted)trace("signal");
 if(clock_ns(CLOCK_MONOTONIC)>=atomic_load(&worker.lease))trace("lease-expired");
 bool orderly=atomic_load(&session_ended)&&!interrupted&&!nt_capture_failed()&&clock_ns(CLOCK_MONOTONIC)<atomic_load(&worker.lease);
 trace("stopping");pf_server_stop(server);pf_server_free(server);trace("stopped");
 bool sealed=!record_view || nt_capture_stop(orderly);
 if(record_view&&!sealed)trace(nt_capture_result());
 if(record_view)trace(sealed?"capture-sealed":"capture-interrupted");
 if(atomic_load(&worker.consumed)){drive_flush(1);json_object *r=rpc("Finalize",NULL,NULL);destroy_json(r);}
 close(worker.control);OPENSSL_cleanse(worker.pin,32);return sealed?0:6;
}
