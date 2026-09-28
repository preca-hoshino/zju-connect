/*
 * Smoke test for the zju-connect C ABI.
 *
 * It links against the shared library produced by
 *   go build -buildmode=c-shared -o libzju_connect.so ./binding/capi
 * and only exercises behaviour that does not require a live VPN server:
 * argument validation, handle lifecycle and state reporting.
 *
 * Build and run (from the repository root):
 *   go build -buildmode=c-shared -o build/libzju_connect.so ./binding/capi
 *   cc -I binding/capi/include -o build/smoke binding/capi/smoke.c \
 *      -L build -lzju_connect -Wl,-rpath,build
 *   ./build/smoke
 *
 * Exit code 0 means every assertion passed.
 */

#include <stdio.h>
#include <string.h>

#include "zju_connect.h"

static int failures = 0;

static void check(int condition, const char *what) {
	if (condition) {
		printf("  ok   %s\n", what);
	} else {
		printf("  FAIL %s\n", what);
		failures++;
	}
}

static void record_event(void *user_data, int type, const char *message) {
	(void)user_data;
	(void)type;
	(void)message;
}

int main(void) {
	printf("zju-connect C ABI smoke test\n");

	/* Invalid JSON must fail and report an error. */
	{
		char *err = NULL;
		zc_client c = zcNew("{not json", NULL, NULL, NULL, &err);
		check(c == 0, "zcNew rejects malformed JSON");
		check(err != NULL && strlen(err) > 0, "zcNew reports an error message");
		zcFreeString(err);
	}

	/* Missing server_address must fail. */
	{
		char *err = NULL;
		zc_client c = zcNew("{\"username\":\"u\"}", NULL, NULL, NULL, &err);
		check(c == 0, "zcNew rejects config without server_address");
		zcFreeString(err);
	}

	/* Valid config creates a handle in the disconnected state. */
	zc_client client = zcNew(
		"{\"server_address\":\"vpn.example.com\",\"username\":\"u\",\"password\":\"p\"}",
		NULL, record_event, NULL, NULL);
	check(client != 0, "zcNew creates a handle for a valid config");
	if (client == 0) {
		return 1;
	}

	char *stat = zcStatJSON(client);
	check(stat != NULL, "zcStatJSON returns a string");
	if (stat != NULL) {
		check(strstr(stat, "\"state\":\"disconnected\"") != NULL,
		      "new session reports the disconnected state");
		zcFreeString(stat);
	}

	/* Zero handle must be rejected, not crash. */
	check(zcSetup(0) == ZC_ERR_INVALID, "zcSetup rejects a zero handle");
	check(zcWritePacket(0, NULL, 0) == ZC_ERR_INVALID, "zcWritePacket rejects a zero handle");
	check(zcRespondChallenge(0, 1, NULL) == ZC_ERR_INVALID,
	      "zcRespondChallenge rejects a zero handle");
	zcClose(0);
	zcFree(0);
	zcFreeString(NULL);

	/* Packet I/O before connect must report an error, not crash. */
	{
		uint8_t buf[1500];
		int n = zcReadPacket(client, buf, (int)sizeof(buf), 10);
		check(n < 0, "zcReadPacket fails before Setup");
	}

	zcClose(client);
	zcFree(client);

	if (failures == 0) {
		printf("all checks passed\n");
		return 0;
	}
	printf("%d check(s) failed\n", failures);
	return 1;
}
