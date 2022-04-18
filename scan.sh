#!/bin/bash

#第一步 go mod vendor

# 将要分析的代码下载到指定目录

runApiScan() {

    url="http://c.biancheng.net/index.html"
    echo ${url#*/}
    echo ${url##*/}
    echo ${url%index*} #结果为 http://c.biancheng.net
    echo ${url%%/*}    #结果为 http:

    url="https://api.github.com/repos/huaweicloud/terraform-provider-huaweicloud/releases/latest"
    echo ${url}

    curl ${url} >releaseVersion.json
    # # 使用正则提取需要的区域信息,实际有需要可以提取省市等接口返回的其他信息
    grep -E '"tag_name"\s*:\s*(.*?),' releaseVersion.json >latest_tmp.info

    res=$(cat latest_tmp.info) # eg: "tag_name":     "v1.26.1",
    res=${res##*:}             #      "v1.26.1",
    res=${res#*\"}             #删除空格以及第一个引号： v1.26.1",

    lenth=${#res}
    echo ${res}
    echo ${lenth}
    res=${res%\"*}

    echo ${res}
    version=${res}
    echo ${res} >latest_version.info
    fileName=${res}".zip"
    echo ${fileName}

    rm -rf ${version}

    downLoadUrl="https://github.com/huaweicloud/terraform-provider-huaweicloud/archive/refs/tags/"${fileName}
    # downLoadUrl="https://github.com/huaweicloud/terraform-provider-huaweicloud/archive/refs/heads/master.zip"
    echo ${downLoadUrl}

    wget ${downLoadUrl} -O ${fileName}
    unzip -oq ${fileName} -d ${version}

    softFiles=$(ls $version)
    srcDir=${softFiles[0]}
    echo ${srcDir}
    cd $version
    cd $srcDir

    ## 将执行脚本copy进来
    res=$(pwd) # /home/hm/GitHub/terraform-api-scan/v1.26.1/terraform-provider-huaweicloud-1.26.1
    echo ${res}
    outputDir=${res}"/api/"
    rm -rf ${outputDir}
    mkdir ${outputDir}
    awk -v line=$(awk '/var allServiceCatalog/{print NR}' huaweicloud/config/endpoints.go) 'BEGIN{print "package config\n\n var AllServiceCatalog = map[string]ServiceCatalog{"}{if(NR>line){print $0}}' huaweicloud/config/endpoints.go >huaweicloud/config/endpoints2.go
    #

    cp ../../main.go ./main.go
    echo ${outputDir} >../../output_dir.info
    subPackPath="/huaweicloud"
    providerSchemaPath="../../schema.json"
    go run main.go -basePath=${res}"/" -outputDir=${outputDir} -version=${version} -providerSchemaPath=${providerSchemaPath}
    ##其他 go run main.go -basePath=${res}"/" -outputDir=${outputDir} -version=${version} -providerSchemaPath=${providerSchemaPath} -provider=flexibleengine
}

runApiScan
