package main

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huaweicloud/terraform-provider-huaweicloud/huaweicloud/config"
)

type CloudUri struct {
	url            string
	httpMethod     string
	resourceType   string
	operationId    string
	filePath       string
	serviceCatalog config.ServiceCatalog
}

func sliceContains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func findAllFunc(f *ast.File, fset *token.FileSet) []*ast.FuncDecl {
	funcs := []*ast.FuncDecl{}

	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			funcs = append(funcs, fn)
		}
	}

	return funcs
}

func removeDuplicateValues(array []string) []string {
	keys := make(map[string]bool)
	list := []string{}

	for _, entry := range array {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func removeDuplicateCloudUri(array []CloudUri) []CloudUri {
	keys := make(map[string]CloudUri)
	list := []string{}
	rt := []CloudUri{}

	for _, v := range array {
		entry := v.url + v.httpMethod + v.resourceType
		entry = strings.ToLower(entry)
		if _, ok := keys[entry]; !ok {
			keys[entry] = v
			list = append(list, entry)
		}
	}
	sort.Strings(list)
	for i := 0; i < len(list); i++ {
		item := list[i]
		rt = append(rt, keys[item])
	}
	return rt
}

func filePathExists(path string) bool {
	_, err := os.Stat(path) //os.Stat获取文件信息
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

func mapToStandardHttpMethod(httpMethod string) string {
	if strings.HasPrefix(httpMethod, "DeleteWith") {
		return "delete"
	}

	return strings.ToLower(httpMethod)
}

func getCatalogFromName(fileName string) (*config.ServiceCatalog, string) {
	var catalog string

	baseName := filepath.Base(fileName)
	parts := strings.Split(baseName, "_")
	length := len(parts)

	if parts[0] == "data" && length >= 4 {
		catalog = parts[3]
	}
	if parts[0] == "resource" && length >= 3 {
		catalog = parts[2]
	}

	if serviceCategory := parseEndPointByClient(catalog); serviceCategory != nil {
		return serviceCategory, serviceCategory.Product
	}
	return nil, strings.ToUpper(catalog)
}

func parseEndPointByClient(clientName string) *config.ServiceCatalog {
	return config.GetServiceCatalog(clientName)
}
