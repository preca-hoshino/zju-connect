#ifndef ZJU_CONNECT_H
#define ZJU_CONNECT_H

/*
 * zju-connect C ABI.
 *
 * This header is the stable interface consumed by the Dart/Flutter FFI layer
 * (through ffigen) and by any other host language. It must be kept in sync with
 * binding/capi/capi.go; the //export directives there generate an equivalent
 * header at build time.
 *
 * Ownership rules:
 *   - Strings returned by this library (zcStatJSON, zcStartProxy) are heap
 *     allocated and must be released with zcFreeString.
 *   - Strings passed into this library are only read during the call.
 *   - zcClose releases runtime resources; zcFree releases the handle itself.
 */

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* zc_client is an opaque session handle. Zero is never valid. */
typedef uintptr_t zc_client;

/* Event types delivered to zc_event_fn. */
enum {
	ZC_EVENT_LOG = 0,
	ZC_EVENT_STATE = 1,
	ZC_EVENT_ERROR = 2,
};

/* Error codes returned by functions that report a negative result. */
enum {
	ZC_OK = 0,
	ZC_ERR_INVALID = -1,
	ZC_ERR_TIMEOUT = -2,
	ZC_ERR_CLOSED = -3,
	ZC_ERR_INTERNAL = -4,
};

/*
 * zc_event_fn receives asynchronous events (logs, state changes, errors).
 *
 * The callback may be invoked from any thread and, for asynchronous hosts such
 * as Dart's NativeCallable.listener, may be delivered after this function has
 * returned. The host therefore OWNS the message string and must release it with
 * zcFreeString once it is done with it.
 */
typedef void (*zc_event_fn)(void *user_data, int type, const char *message);

/*
 * zc_challenge_fn receives an authentication challenge. The host must answer it
 * with zcRespondChallenge using the same challenge_id. payload is a JSON object
 * whose schema depends on the challenge kind. Ownership of payload transfers to
 * the host, which must release it with zcFreeString.
 */
typedef void (*zc_challenge_fn)(void *user_data, int64_t challenge_id, const char *payload);

/*
 * zcNew creates a session from a JSON configuration (see binding.Config).
 *
 * The callbacks may be NULL. user_data is passed back verbatim.
 *
 * Returns 0 on failure; when err_out is non-NULL it receives a message that
 * must be freed with zcFreeString.
 */
zc_client zcNew(const char *config_json, void *user_data, zc_event_fn on_event,
                zc_challenge_fn on_challenge, char **err_out);

/* zcSetup performs the VPN login. Returns ZC_OK or a negative code. Blocks. */
int zcSetup(zc_client client);

/* zcClose tears the session down. Safe to call multiple times. */
void zcClose(zc_client client);

/* zcFree releases the handle. Call after zcClose. */
void zcFree(zc_client client);

/*
 * zcStatJSON returns {"state","virtual_ip","session_id","client_data"}.
 * Free the result with zcFreeString.
 */
char *zcStatJSON(zc_client client);

/*
 * zcStartProxy starts the local proxies from a JSON ProxyConfig and returns
 * {"socks","http"} with the bound addresses. Free with zcFreeString.
 */
char *zcStartProxy(zc_client client, const char *proxy_config_json);

/*
 * zcReadPacket reads one IPv4 packet into buf. Returns the byte count or a
 * negative code (ZC_ERR_TIMEOUT, ZC_ERR_CLOSED, ...). Blocks up to timeout_ms;
 * zero means forever.
 */
int zcReadPacket(zc_client client, uint8_t *buf, int length, int timeout_ms);

/* zcWritePacket sends one IPv4 packet. Returns ZC_OK or a negative code. */
int zcWritePacket(zc_client client, const uint8_t *buf, int length);

/*
 * zcRespondChallenge answers a challenge. response_json is a JSON object.
 * Returns ZC_OK or ZC_ERR_INVALID when the id is unknown.
 */
int zcRespondChallenge(zc_client client, int64_t challenge_id, const char *response_json);

/* zcFreeString frees a string returned by this library. NULL is ignored. */
void zcFreeString(char *str);

#ifdef __cplusplus
}
#endif

#endif /* ZJU_CONNECT_H */
