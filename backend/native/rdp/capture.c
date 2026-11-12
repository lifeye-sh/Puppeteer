#define _GNU_SOURCE
#include "capture.h"
#include <arpa/inet.h>
#include <endian.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <stdint.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#define PERIOD_NS 100000000LL
#define WRITE_NS 2000000000LL
#define FIRST_PAINT_NS 4000000000LL
// At 1080p the queue is bounded to 16 * 7.91 MiB. A scheduling or encoding
// stall beyond this window interrupts the session instead of dropping samples.
#define QUEUE_SIZE 16
struct sampled_frame { uint8_t *pixels; size_t capacity; unsigned w,h; };
static struct {
    pthread_mutex_t lock;
    pthread_cond_t changed;
    pthread_t writer;
    struct sampled_frame slots[QUEUE_SIZE];
    unsigned head,count;
    bool closing;
} queue = {.lock=PTHREAD_MUTEX_INITIALIZER,.changed=PTHREAD_COND_INITIALIZER};
static _Atomic(const char *) result = "capture-not-started";
static bool enqueue_due(int64_t now);
static atomic_bool write_failed;
const char *nt_capture_result(void) { return result; }

static struct {
    pthread_mutex_t lock;
    pthread_t thread;
    uint8_t *pixels;
    size_t capacity;
    unsigned width, height;
    int fd;
    bool started;
    atomic_bool running, failed;
    uint64_t sequence;
    int64_t next;
} capture = { .lock = PTHREAD_MUTEX_INITIALIZER, .fd = -1 };

static int64_t now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static bool transfer(void *buffer, size_t bytes, bool output, int64_t deadline) {
    uint8_t *ptr = buffer;
    while (bytes) {
        int64_t left = deadline - now_ns();
        if (left <= 0) return false;
        struct pollfd p = { .fd = capture.fd, .events = output ? POLLOUT : POLLIN };
        int ready = poll(&p, 1, (int)((left + 999999) / 1000000));
        if (ready < 0 && errno == EINTR) continue;
        if (ready <= 0 || !(p.revents & p.events)) return false;
        ssize_t n = output ? send(capture.fd, ptr, bytes, MSG_NOSIGNAL | MSG_DONTWAIT)
                           : recv(capture.fd, ptr, bytes, MSG_DONTWAIT);
        if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
        if (n <= 0) return false;
        ptr += n;
        bytes -= (size_t)n;
    }
    return true;
}

static bool packet(unsigned width, unsigned height, uint8_t *pixels) {
    uint8_t header[24] = { 'N','T','R','F', 1, pixels ? 1 : 2 };
    uint64_t seq = htobe64(capture.sequence);
    uint16_t w = htons(width), h = htons(height);
    uint32_t bytes = width * height * 4U, length = htonl(bytes);
    memcpy(header + 8, &seq, 8);
    memcpy(header + 16, &w, 2);
    memcpy(header + 18, &h, 2);
    memcpy(header + 20, &length, 4);
    const int64_t deadline = now_ns() + WRITE_NS;
    return transfer(header, sizeof(header), true, deadline) &&
           transfer(pixels, bytes, true, deadline);
}

/* EndPaint is called on the rendering thread. The sampler never dereferences
 * an SDK framebuffer while the decoder may resize or free it. */
bool nt_capture_publish(const rdpGdi *gdi) {
    if (!gdi || !gdi->primary_buffer || gdi->dstFormat != PIXEL_FORMAT_BGRA32 ||
        gdi->width < 2 || gdi->width > 1920 || gdi->height < 2 || gdi->height > 1080 ||
        gdi->stride < (unsigned)gdi->width * 4) {
        result="capture-invalid-framebuffer";
        atomic_store(&capture.failed,true);
        return false;
    }
    pthread_mutex_lock(&capture.lock);
    const size_t row = (size_t)gdi->width * 4, bytes = row * gdi->height;
    int64_t now=now_ns();
    bool ok = atomic_load(&capture.running) && !atomic_load(&capture.failed) && enqueue_due(now);
    if (!capture.width) capture.next=now;
    if (ok && bytes > capture.capacity) {
        uint8_t *next = realloc(capture.pixels, bytes);
        if (!next) ok = false;
        else { capture.pixels = next; capture.capacity = bytes; }
    }
    if (ok) {
        for (int y = 0; y < gdi->height; ++y)
            memcpy(capture.pixels + y * row, gdi->primary_buffer + (size_t)y * gdi->stride, row);
        capture.width = gdi->width;
        capture.height = gdi->height;
        ok=enqueue_due(now);
    }
    if(!ok)atomic_store(&capture.failed,true);
    pthread_mutex_unlock(&capture.lock);
    return ok;
}

static void *write_frames(void *unused) {
    (void)unused;
    pthread_mutex_lock(&queue.lock);
    while (!queue.closing || queue.count) {
        if (atomic_load(&capture.failed)) break;
        if (!queue.count) {
            pthread_cond_wait(&queue.changed,&queue.lock);
            continue;
        }
        struct sampled_frame *frame=&queue.slots[queue.head];
        pthread_mutex_unlock(&queue.lock);
        bool ok=packet(frame->w,frame->h,frame->pixels);
        pthread_mutex_lock(&queue.lock);
        if (!ok) {
            atomic_store(&write_failed,true);
            atomic_store(&capture.failed,true);
            break;
        }
        ++capture.sequence;
        queue.head=(queue.head+1)%QUEUE_SIZE;
        --queue.count;
    }
    pthread_mutex_unlock(&queue.lock);
    return NULL;
}

// Charge every elapsed sampling instant to the image that was visible then.
// Both EndPaint and the idle clock advance this timeline under capture.lock.
// Scheduler jitter during a static screen can be recovered exactly. A full
// queue is an error and the session is interrupted.
static bool enqueue_due(int64_t now) {
    if (!capture.width) return true;
    size_t bytes=(size_t)capture.width*capture.height*4;
    pthread_mutex_lock(&queue.lock);
    while (capture.next <= now) {
        if (queue.count==QUEUE_SIZE) {
            result="capture-queue-full";
            pthread_mutex_unlock(&queue.lock);
            return false;
        }
        struct sampled_frame *frame=&queue.slots[(queue.head+queue.count)%QUEUE_SIZE];
        if (bytes>frame->capacity) {
            uint8_t *larger=realloc(frame->pixels,bytes);
            if (!larger) {
                result="capture-out-of-memory";
                pthread_mutex_unlock(&queue.lock);
                return false;
            }
            frame->pixels=larger;
            frame->capacity=bytes;
        }
        memcpy(frame->pixels,capture.pixels,bytes);
        frame->w=capture.width;
        frame->h=capture.height;
        ++queue.count;
        capture.next+=PERIOD_NS;
    }
    pthread_cond_signal(&queue.changed);
    pthread_mutex_unlock(&queue.lock);
    return true;
}

static void *sample(void *unused) {
    (void)unused;
    const int64_t first_deadline=now_ns()+FIRST_PAINT_NS;
    while (atomic_load(&capture.running) && !atomic_load(&capture.failed)) {
        pthread_mutex_lock(&capture.lock);
        int64_t now=now_ns();
        bool ok=enqueue_due(now);
        if (!capture.width && now>=first_deadline) { result="capture-no-frame"; ok=false; }
        int64_t next=capture.width?capture.next:now+PERIOD_NS;
        pthread_mutex_unlock(&capture.lock);
        if (!ok) { atomic_store(&capture.failed,true); break; }
        struct timespec target={ next/1000000000LL,next%1000000000LL };
        int err;
        do { err=clock_nanosleep(CLOCK_MONOTONIC,TIMER_ABSTIME,&target,NULL); } while(err==EINTR);
        if(err) { result="capture-clock-failed"; atomic_store(&capture.failed,true); break; }
    }
    return NULL;
}

bool nt_capture_start(int fd) {
    if (capture.started || fd < 0) { if (fd >= 0) close(fd); return false; }
    capture.fd = fd;
    char ready = 0;
    if (!transfer(&ready, 1, false, now_ns() + FIRST_PAINT_NS) || ready != 'R') goto fail;
    atomic_store(&capture.running, true);
    if (pthread_create(&queue.writer,NULL,write_frames,NULL)!=0) goto fail;
    if (pthread_create(&capture.thread, NULL, sample, NULL) != 0) {
        pthread_mutex_lock(&queue.lock);
        queue.closing=true;
        pthread_cond_signal(&queue.changed);
        pthread_mutex_unlock(&queue.lock);
        pthread_join(queue.writer,NULL);
        goto fail;
    }
    capture.started = true;
    return true;
fail:
    atomic_store(&capture.running, false);
    close(fd);
    capture.fd = -1;
    return false;
}

bool nt_capture_failed(void) { return atomic_load(&capture.failed); }

bool nt_capture_stop(bool orderly) {
    if (!capture.started) return false;
    atomic_store(&capture.running, false);
    pthread_join(capture.thread, NULL);
    if (!orderly || nt_capture_failed()) shutdown(capture.fd,SHUT_RDWR);
    pthread_mutex_lock(&queue.lock);
    queue.closing=true;
    pthread_cond_signal(&queue.changed);
    pthread_mutex_unlock(&queue.lock);
    pthread_join(queue.writer,NULL);
    for(unsigned i=0;i<QUEUE_SIZE;++i)free(queue.slots[i].pixels);
    if(atomic_load(&write_failed))result="capture-write-failed";
    char ack = 0;
    if(!nt_capture_failed())result=!orderly?"capture-aborted":capture.sequence?"capture-ack-failed":"capture-empty";
    bool ok = orderly && !nt_capture_failed() && capture.sequence > 0 &&
              packet(0, 0, NULL) &&
              transfer(&ack, 1, false, now_ns() + 5000000000LL) && ack == 'C';
    close(capture.fd);
    free(capture.pixels);
    capture.pixels = NULL;
    capture.started = false;
    return ok;
}
