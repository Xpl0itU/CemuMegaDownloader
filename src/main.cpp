#include <downloader.h>
#include <iostream>
#include <filesystem>
#include <dirent.h>
#include <fstream>

#include <cdecrypt/cdecrypt.h>
#include <json/json.h>

#define REGION_EUR 0
#define REGION_USA 1
#define REGION_JPN 2

void removeFiles(const std::string& path) {
    for(const auto & entry : std::filesystem::directory_iterator(path)) {
        if(entry.is_regular_file()) {
            std::filesystem::remove(entry.path());
        }
    }
}

int main() {
    std::ifstream titlesJson("titles.json");
    if(!titlesJson.is_open()) {
        std::cout << "titles.json file not found!\n";
        return 1;
    }
    Json::Value root;
    titlesJson >> root;
    Json::Value regionArray[3] = {root["EUR"], root["USA"], root["JPN"]};
    int choice;
    while (true) {
        std::cout << "1. Download EUR titles" << std::endl;
        std::cout << "2. Download USA titles" << std::endl;
        std::cout << "3. Download JPN titles" << std::endl;
        std::cout << "4. Exit" << std::endl;
        std::cout << "Select an option: ";
        std::cin >> choice;

        switch (choice) {
            case 1: // EUR
                for (int i = 0; i < regionArray[REGION_EUR].size(); ++i) {
                    char *outputPath = strdup(regionArray[REGION_EUR][i].get("path", "./mlc01/sys/title/00050010/1004A200").asCString());
                    downloadTitle(regionArray[REGION_EUR][i].get("titleID", "000500101004A200").asCString(), outputPath);
                    char *argv[2] = {"CemuMegaDownloader", outputPath};
                    if (cdecrypt(2, argv) != 0) {
                        std::cout << "Error: There was a problem decrypting the files.\nThe path specified for the download might be too long.\nPlease try downloading the files to a shorter path and try again.\n";
                        free(outputPath);
                        goto exit;
                    }
                    removeFiles(outputPath);
                    free(outputPath);
                }
                std::cout << "EUR titles downloaded" << std::endl;
                goto exit;
            case 2: // USA
                for (int i = 0; i < regionArray[REGION_USA].size(); ++i) {
                    char *outputPath = strdup(regionArray[REGION_USA][i].get("path", "./mlc01/sys/title/00050010/1004A200").asCString());
                    downloadTitle(regionArray[REGION_USA][i].get("titleID", "000500101004A200").asCString(), outputPath);
                    char *argv[2] = {"CemuMegaDownloader", outputPath};
                    if (cdecrypt(2, argv) != 0) {
                        std::cout << "Error: There was a problem decrypting the files.\nThe path specified for the download might be too long.\nPlease try downloading the files to a shorter path and try again.\n";
                        free(outputPath);
                        goto exit;
                    }
                    removeFiles(outputPath);
                    free(outputPath);
                }
                std::cout << "USA titles downloaded" << std::endl;
                goto exit;
            case 3: // JPN
                for (int i = 0; i < regionArray[REGION_JPN].size(); ++i) {
                    char *outputPath = strdup(regionArray[REGION_JPN][i].get("path", "./mlc01/sys/title/00050010/1004A200").asCString());
                    downloadTitle(regionArray[REGION_JPN][i].get("titleID", "000500101004A200").asCString(), outputPath);
                    char *argv[2] = {"CemuMegaDownloader", outputPath};
                    if (cdecrypt(2, argv) != 0) {
                        std::cout << "Error: There was a problem decrypting the files.\nThe path specified for the download might be too long.\nPlease try downloading the files to a shorter path and try again.\n";
                        free(outputPath);
                        goto exit;
                    }
                    removeFiles(outputPath);
                    free(outputPath);
                }
                std::cout << "JPN titles downloaded" << std::endl;
                goto exit;
            case 4: // Exit
                goto exit;
            default: // Invalid option selected
                std::cout << "Invalid choice. Try again." << std::endl;
                break;
        }
    }
exit:
    std::cout << "Press any key to exit...\n";
    std::cin.ignore();
    std::cin.get();
    titlesJson.close();
    return 0;
}