#include <mbedtls/aes.h>
#include <mbedtls/md5.h>
#include <mbedtls/pkcs5.h>

#include <stdlib.h>
#include <string.h>

#include <keygen.h>

#define KEYGEN_SECRET "fd040105060b111c2d49"

static const uint8_t keygen_pw[] = {0x6d, 0x79, 0x70, 0x61, 0x73, 0x73};
static const uint8_t commonKey[16] = {0xd7, 0xb0, 0x04, 0x02, 0x65, 0x9b, 0xa2, 0xab, 0xd2, 0xcb, 0x0d, 0xb2, 0x7f, 0xa2, 0xb6, 0x56};

static bool encryptAES(void *data, int data_len, const unsigned char *key, unsigned char *iv, void *encrypted) {
    mbedtls_aes_context ctx;
    mbedtls_aes_init(&ctx);
    mbedtls_aes_setkey_enc(&ctx, key, 128);
    bool ret = mbedtls_aes_crypt_cbc(&ctx, MBEDTLS_AES_ENCRYPT, data_len, iv, data, encrypted) == 0;
    /*
     * We're not calling mbedtls_aes_free() as at the time of writing
     * all it does is overwriting the mbedtls_aes_context struct with
     * zeros.
     * As game key calculation isn't top secret we don't need this.
     *
     * TODO: Check the codes at every mbed TLS update to make sure
     * we won't need to call it.
     */
    // mbedtls_aes_free(&ctx);
    return ret;
}

bool generateKey(const char *tid, char *out) {
    char *ret = malloc(33);
    if (ret == NULL)
        return false;

    char *tmp = tid;
    while (tmp[0] == '0' && tmp[1] == '0')
        tmp += 2;

    char h[1024];
    strcpy(h, KEYGEN_SECRET);
    strcat(h, tmp);

    size_t bhl = strlen(h) >> 1;
    uint8_t bh[bhl];
    for (size_t i = 0, j = 0; j < bhl; i += 2, j++)
        bh[j] = (h[i] % 32 + 9) % 25 * 16 + (h[i + 1] % 32 + 9) % 25;

    uint8_t md5sum[16];
    mbedtls_md5(bh, bhl, md5sum);

    uint8_t key[16];
    mbedtls_md_context_t ctx;
    mbedtls_md_setup(&ctx, mbedtls_md_info_from_type(MBEDTLS_MD_SHA1), 1);
    if (mbedtls_pkcs5_pbkdf2_hmac(&ctx, (const unsigned char *) keygen_pw, sizeof(keygen_pw), md5sum, 16, 20, 16, key) != 0)
        return false;

    uint8_t iv[16];
    for (size_t i = 0, j = 0; j < 8; i += 2, j++)
        iv[j] = (tid[i] % 32 + 9) % 25 * 16 + (tid[i + 1] % 32 + 9) % 25;

    memset(&iv[8], 0, 8);
    encryptAES(key, 16, commonKey, iv, key);

    tmp = ret;
    for (int i = 0; i < 16; i++, tmp += 2)
        sprintf(tmp, "%02x", key[i]);

    sprintf(out, "%s", ret);

    return ret != NULL;
}