package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/chnsz/scan-autogen-code/model"
	"gopkg.in/yaml.v3"
)

var inputDir string
var outputDir string
var version string

func init() {
	flag.StringVar(&inputDir, "inputDir", "./input", "The input dir of auto-gen resource yaml")
	flag.StringVar(&outputDir, "outputDir", "./output", "api yaml file output Dir")
	flag.StringVar(&version, "version", "", "provider version")
}

func main() {
	flag.Parse()
	files := GetAllFiles(inputDir, ".yaml")
	for _, v := range files {
		convert(v, outputDir)
	}
}

// 获取指定目录下的所有文件,包含子目录下的文件
func GetAllFiles(dirPth string, suffix string) (files []string) {
	dir, err := os.ReadDir(dirPth)
	if err != nil {
		log.Printf("error: %v", err)
	}

	sep := string(os.PathSeparator)
	for _, entry := range dir {
		if entry.IsDir() {
			files = append(files, GetAllFiles(dirPth+sep+entry.Name(), suffix)...)
		} else {
			ok := strings.HasSuffix(entry.Name(), suffix)
			if ok {
				files = append(files, dirPth+sep+entry.Name())
			}
		}
	}
	return files
}

func convert(filePath string, outputDir string) {
	inputContent, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Printf("error: %v", err)
	}

	var inputApi model.Api
	err = yaml.Unmarshal(inputContent, &inputApi)
	if err != nil {
		log.Printf("error: %v", err)
	}
	index := strings.LastIndex(filePath, "/")
	if index > 0 {
		index = index + 1
	}
	fileName := filePath[index:]
	outputApi := convertApi(inputApi, strings.TrimSuffix(fileName, ".yaml"))
	outputContent, err := yaml.Marshal(outputApi)
	if err != nil {
		log.Printf("error: %v", err)
	}

	sep := string(os.PathSeparator)
	err = ioutil.WriteFile(outputDir+sep+fileName, outputContent, os.ModePerm)
	if err != nil {
		log.Printf("error: %v", err)
	}
}

func convertApi(inputApi model.Api, resourceName string) model.Api {
	var outputApi model.Api
	outputApi.Info = inputApi.Info
	outputApi.Info.Title = resourceName
	outputApi.Servers = inputApi.Servers
	outputApi.Host = "myhuaweicloud.com"
	outputApi.Paths = convertPath(inputApi.Paths)
	outputApi.Tags = parseTags(inputApi.Paths)
	return outputApi
}

func convertPath(paths map[string]map[string]model.OperationInfo) map[string]map[string]model.OperationInfo {
	rst := make(map[string]map[string]model.OperationInfo)
	for _, path := range paths {
		for _, operation := range path {
			apiUrl := strings.Split(operation.XrefApi, " ")
			if len(apiUrl) != 2 {
				log.Println("error bad x-ref-api 格式不正确.", operation.OperationId, operation.XrefApi)
				continue
			}

			if _, ok := rst[apiUrl[1]]; !ok {
				v := make(map[string]model.OperationInfo)
				v[apiUrl[0]] = model.OperationInfo{
					Tag:         operation.XrefProduct,
					OperationId: operation.OperationId,
					XrefApi:     operation.XrefApi,
				}
				rst[apiUrl[1]] = v
			} else {
				rst[apiUrl[1]][apiUrl[0]] = model.OperationInfo{
					Tag:         operation.XrefProduct,
					OperationId: operation.OperationId,
					XrefApi:     operation.XrefApi,
				}
			}

		}
	}
	return rst
}

// 得到所有x-ref-product并去重
func parseTags(paths map[string]map[string]model.OperationInfo) []model.Tag {
	tagSet := make(map[string]string)
	for _, path := range paths {
		for _, operation := range path {
			tagSet[operation.XrefProduct] = "1"
		}
	}
	rst := make([]model.Tag, 0, len(tagSet))
	for k := range tagSet {
		rst = append(rst, model.Tag{Name: k})
	}
	return rst
}
