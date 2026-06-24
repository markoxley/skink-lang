// Stub implementations for libmosquitto functions.
// Used when the real libmosquitto is not available.
#include <string.h>

int mosquitto_lib_init(void) { return 0; }
int mosquitto_lib_cleanup(void) { return 0; }

struct mosquitto {
  int dummy;
};

static struct mosquitto dummy_client = {0};

struct mosquitto *mosquitto_new(const char *id, int clean_session, void *obj) {
  (void)id;
  (void)clean_session;
  (void)obj;
  return &dummy_client; // Return dummy client for smoke tests
}

void mosquitto_destroy(struct mosquitto *mosq) { (void)mosq; }

int mosquitto_connect(struct mosquitto *mosq, const char *host, int port,
                      int keepalive) {
  (void)mosq;
  (void)host;
  (void)port;
  (void)keepalive;
  return 1; // Return error (tests handle errors)
}

int mosquitto_disconnect(struct mosquitto *mosq) {
  (void)mosq;
  return 1;
}

int mosquitto_publish(struct mosquitto *mosq, int *mid, const char *topic,
                      int payloadlen, const void *payload, int qos,
                      int retain) {
  (void)mosq;
  (void)mid;
  (void)topic;
  (void)payloadlen;
  (void)payload;
  (void)qos;
  (void)retain;
  return 1;
}

int mosquitto_subscribe(struct mosquitto *mosq, int *mid, const char *topic,
                        int qos) {
  (void)mosq;
  (void)mid;
  (void)topic;
  (void)qos;
  return 1;
}

int mosquitto_loop_start(struct mosquitto *mosq) {
  (void)mosq;
  return 1;
}

int mosquitto_loop_stop(struct mosquitto *mosq, int force) {
  (void)mosq;
  (void)force;
  return 1;
}

int mosquitto_loop(struct mosquitto *mosq, int timeout, int max_packets) {
  (void)mosq;
  (void)timeout;
  (void)max_packets;
  return 1;
}
