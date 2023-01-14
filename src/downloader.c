#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <inttypes.h>
#include <math.h>
#include <unistd.h>
#ifndef _WIN32
#    include <sys/stat.h>
#endif

#include <curl/curl.h>

#include <downloader.h>
#include <keygen.h>
#include <utils.h>

struct MemoryStruct {
  uint8_t* memory;
  size_t size;
};

struct PathFileStruct {
    char* file_path;
    FILE* file_pointer;
};

int progress_func(void *p,
             curl_off_t dltotal, curl_off_t dlnow,
             curl_off_t ultotal, curl_off_t ulnow)
{
    printf("Progress: %" CURL_FORMAT_CURL_OFF_T " of %" CURL_FORMAT_CURL_OFF_T, dlnow, dltotal);
    printf("\r");
    fflush(stdout);
    return 0;
}

static size_t WriteDataToMemory(void* contents, size_t size, size_t nmemb, void* userp)
{
    size_t realsize = size * nmemb;
    struct MemoryStruct* mem = (struct MemoryStruct*) userp;

    mem->memory = realloc(mem->memory, mem->size + realsize);
    memcpy(&(mem->memory[mem->size]), contents, realsize);
    mem->size += realsize;

    return realsize;
}

static int char2int(char input)
{
    if (input >= '0' && input <= '9')
        return input - '0';
    if (input >= 'A' && input <= 'F')
        return input - 'A' + 10;
    if (input >= 'a' && input <= 'f')
        return input - 'a' + 10;
    fprintf(stderr, "Error: Malformed input.\n");
    exit(EXIT_FAILURE);
}

static void hex2bytes(const char* input, uint8_t* output)
{
    int input_length = strlen(input);
    for (int i = 0; i < input_length; i += 2) {
        output[i / 2] = char2int(input[i]) * 16 + char2int(input[i + 1]);
    }
}

static void create_ticket(const char* title_id, const char* title_key, uint16_t title_version, const char* output_path)
{
    FILE* ticket_file = fopen(output_path, "wb");
    if (!ticket_file) {
        fprintf(stderr, "Error: The file \"%s\" couldn't be opened. Will exit now.\n", output_path);
        exit(EXIT_FAILURE);
    }

    uint8_t ticket_data[848] = "\x00\x01\x00\x04\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\xd1\x5e\xa5\xed\x15\xab\xe1\x1a\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x52\x6f\x6f\x74\x2d\x43\x41\x30\x30\x30\x30\x30\x30\x30\x33\x2d\x58\x53\x30\x30\x30\x30\x30\x30\x30\x63\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\xfe\xed\xfa\xce\x01\x00\x00\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\xcc\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\xaa\xaa\xaa\xaa\xaa\xaa\xaa\xaa\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x14\x00\x00\x00\xac\x00\x00\x00\x14\x00\x01\x00\x14\x00\x00\x00\x00\x00\x00\x00\x28\x00\x00\x00\x01\x00\x00\x00\x84\x00\x00\x00\x84\x00\x03\x00\x00\x00\x00\x00\x00\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00";
    // fill in the values from the title_id and title_key
    hex2bytes(title_id, &ticket_data[476]);
    hex2bytes(title_key, &ticket_data[447]);
    memcpy(&ticket_data[486], &title_version, 2);
    fwrite(ticket_data, 1, 848, ticket_file);
    fclose(ticket_file);
    printf("Finished creating \"%s\".\n", output_path);
}

static int downloadFile(const char *download_url, const char *output_path) {
    CURL* new_handle = curl_easy_init();
    FILE *file = fopen(output_path, "wb");
    if (file == NULL)
        return 1;
    curl_easy_setopt(new_handle, CURLOPT_FAILONERROR, 1L);

    curl_easy_setopt(new_handle, CURLOPT_WRITEFUNCTION, fwrite);
    curl_easy_setopt(new_handle, CURLOPT_URL, download_url);
    curl_easy_setopt(new_handle, CURLOPT_NOPROGRESS, 0L);
    curl_easy_setopt(new_handle, CURLOPT_XFERINFOFUNCTION, progress_func);

    curl_easy_setopt(new_handle, CURLOPT_WRITEDATA, file);

    curl_easy_setopt(new_handle, CURLOPT_SSL_VERIFYPEER, false);
    curl_easy_setopt(new_handle, CURLOPT_SSL_VERIFYHOST, false);
    curl_easy_setopt(new_handle, CURLOPT_ACCEPTTIMEOUT_MS, 5);
    curl_easy_setopt(new_handle, CURLOPT_TCP_KEEPALIVE, 1L);
    curl_easy_setopt(new_handle, CURLOPT_TCP_NODELAY, 1);
    curl_easy_setopt(new_handle, CURLOPT_CONNECTTIMEOUT, 5);
    curl_easy_setopt(new_handle, CURLOPT_IPRESOLVE, CURL_IPRESOLVE_V4);
    curl_easy_setopt(new_handle, CURLOPT_NOSIGNAL, 1);

    curl_easy_setopt(new_handle, CURLOPT_TCP_KEEPALIVE, 1L);
    curl_easy_setopt(new_handle, CURLOPT_TCP_KEEPIDLE, 120L);
    curl_easy_setopt(new_handle, CURLOPT_TCP_KEEPINTVL, 60L);

    curl_easy_setopt(new_handle, CURLOPT_FAILONERROR, 1L);

    printf("Downloading %s\n", download_url);

    CURLcode curlCode = curl_easy_perform(new_handle);

    long httpCode = 0;
    curl_easy_getinfo(new_handle, CURLINFO_RESPONSE_CODE, &httpCode);

    curl_easy_cleanup(new_handle);

    if (httpCode != 200 || curlCode != CURLE_OK && curlCode != CURLE_WRITE_ERROR) {
        fclose(file);
        return 1;
    }

    fclose(file);
    return 0;
}

static void mkdir_p(const char *dir) {
    char tmp[256];
    char *p = NULL;
    size_t len;

    snprintf(tmp, sizeof(tmp),"%s",dir);
    len = strlen(tmp);
    if (tmp[len - 1] == '/')
        tmp[len - 1] = 0;
    for (p = tmp + 1; *p; p++)
        if (*p == '/') {
            *p = 0;
            #ifdef _WIN32
                mkdir(tmp);
            #else
                mkdir(tmp, S_IRWXU);
            #endif
            *p = '/';
        }
    #ifdef _WIN32
        mkdir(tmp);
    #else
        mkdir(tmp, S_IRWXU);
    #endif
}

int downloadTitle(const char *titleID, const char *outputDirectory) {
    // initialize some useful variables
    char* output_dir = strdup(outputDirectory);
    if (output_dir[strlen(output_dir)-1] == '/' || output_dir[strlen(output_dir)-1] == '\\') {
        output_dir[strlen(output_dir)-1] = '\0';
    }
    char *base_url = malloc(69);
    snprintf(base_url, 69, "http://ccs.cdn.c.shop.nintendowifi.net/ccs/download/%s", titleID);
    char download_url[81];
    char output_path[strlen(output_dir) + 14];

    // create the output directory if it doesn't exist
    mkdir_p(output_dir);

    // initialize curl
    curl_global_init(CURL_GLOBAL_ALL);

    // make an own handle for the tmd file, as we wanna download it to memory first
    CURL* tmd_handle = curl_easy_init();
    curl_easy_setopt(tmd_handle, CURLOPT_FAILONERROR, 1L);

    // Download the tmd and save it in memory, as we need some data from it
    curl_easy_setopt(tmd_handle, CURLOPT_WRITEFUNCTION, WriteDataToMemory);
    snprintf(download_url, 73, "%s/%s", base_url, "tmd");
    curl_easy_setopt(tmd_handle, CURLOPT_URL, download_url);

    struct MemoryStruct tmd_data;
    tmd_data.memory = malloc(0);
    tmd_data.size = 0;
    curl_easy_setopt(tmd_handle, CURLOPT_WRITEDATA, (void*) &tmd_data);
    curl_easy_perform(tmd_handle);
    curl_easy_cleanup(tmd_handle);
    // write out the tmd file
    snprintf(output_path, sizeof(output_path), "%s/%s", output_dir, "title.tmd");
    FILE* tmd_file = fopen(output_path, "wb");
    if (!tmd_file) {
        free(base_url);
        free(output_dir);
        fprintf(stderr, "Error: The file \"%s\" couldn't be opened. Will exit now.\n", output_path);
        exit(EXIT_FAILURE);
    }
    fwrite(tmd_data.memory, 1, tmd_data.size, tmd_file);
    fclose(tmd_file);
    printf("Finished downloading \"%s\".\n", output_path);

    uint16_t title_version;
    memcpy(&title_version, &tmd_data.memory[476], 2);
    snprintf(output_path, sizeof(output_path), "%s/%s", output_dir, "title.tik");
    snprintf(download_url, 74, "%s/%s", base_url, "cetk");
    downloadFile(download_url, output_path);

    uint16_t content_count;
    memcpy(&content_count, &tmd_data.memory[478], 2);
    content_count = bswap_16(content_count);

    // Add all needed curl handles to the multi handle
    for (int i = 0; i < content_count; i++) {
        int offset = 2820 + (48 * i);
        uint32_t id; // the id should usually be chronological, but we wanna be sure
        memcpy(&id, &tmd_data.memory[offset], 4);
        id = bswap_32(id);

        // add a curl handle for the content file (.app file)
        snprintf(output_path, sizeof(output_path), "%s/%08X.app", output_dir, id);
        snprintf(download_url, 78, "%s/%08X", base_url, id);
        downloadFile(download_url, output_path);

        if ((tmd_data.memory[offset + 7] & 0x2) == 2) {
            // add a curl handle for the hash file (.h3 file)
            snprintf(output_path, sizeof(output_path), "%s/%08X.h3", output_dir, id);
            snprintf(download_url, 81, "%s/%08X.h3", base_url, id);
            downloadFile(download_url, output_path);
        }
    }
    free(base_url);
    free(tmd_data.memory);

    printf("Downloading all files for TitleID %s done...\n", titleID);

    // cleanup curl stuff
    curl_global_cleanup();
    free(output_dir);
}