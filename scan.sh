#!/usr/bin/env bash

LatestFile="latest_version.info"
LatestVersion=""
LatestProviderSpace=""

function get_latest_version() {
    url="https://api.github.com/repos/huaweicloud/terraform-provider-huaweicloud/releases/latest"
    all_releases="tmp_releases.json"

    echo "get the latest release from ${url}"
    curl ${url} >${all_releases} 2>/dev/null

    LatestVersion=$(grep -E '"tag_name":' ${all_releases} | awk -F "\"" '{print $4}')
    echo "the latest release of terraform-provider-huaweicloud is ${LatestVersion}"

    rm -f ${all_releases}
    echo ${LatestVersion} >${LatestFile}
}

function download_version() {
    version=$1
    fileName="${LatestVersion}.zip"
    downLoadUrl="https://github.com/huaweicloud/terraform-provider-huaweicloud/archive/refs/tags/"${fileName}

    rm -rf ${version} ${fileName}
    if [ $? -ne 0 ]; then
        echo "ERROR: failed to cleanup the codes of ${version}"
        exit -1
    fi

    echo -e "\ndownload the codes of ${version} from ${downLoadUrl}"
    wget ${downLoadUrl} -O ${fileName}
    if [ $? -ne 0 ]; then
        echo "ERROR: failed to download the codes of ${version}"
        exit -1
    fi

    echo -e "\nunzip ${fileName}..."
    unzip -oq ${fileName} -d ${version}
    if [ $? -ne 0 ]; then
        echo "ERROR: failed to unzip ${fileName}"
        exit -1
    fi

    softFiles=$(ls $version)
    srcDir=${softFiles[0]}
    # ./v1.xx.0/terraform-provider-huaweicloud-1.xx.0
    LatestProviderSpace="./$version/$srcDir"
    echo "the working space of terraform-provider-huaweicloud ${version} is ${LatestProviderSpace}"
}

function do_api_scan() {
    providerSpace=$1
    version=$2
    if [ ! -d $providerSpace ]; then
        echo "ERROR: $providerSpace is not exist or an directory "
        exit -1
    fi

    cp *.go $providerSpace
    cd $providerSpace
    rm -f *_test.go

    outputDir="./api/"
    rm -rf ${outputDir}
    mkdir ${outputDir}
    
    providerSchemaPath="../../schema.json"
    go run *.go -basePath="./" -outputDir=${outputDir} -version=${version} -providerSchemaPath=${providerSchemaPath}
    cd -
}

# execution environment checks
go version
if [ $? -ne 0 ]; then
    echo "ERROR: go command not found"
    exit 1
fi

get_latest_version
if [ "X$LatestVersion" == "X" ]; then
    echo "ERROR: failed to get the latest release"
    exit -1
fi

download_version $LatestVersion
if [ "X$LatestProviderSpace" == "X" ]; then
    echo "ERROR: failed to download the $LatestVersion release"
    exit -1
fi

echo "====== Begin to parse APIs used by the provider ======"
do_api_scan $LatestProviderSpace $LatestVersion
echo "====== End to parse APIs used by the provider ======"

exit 0
