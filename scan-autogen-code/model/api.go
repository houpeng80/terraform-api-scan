package model

type Api struct {
	Info    Info                                `yaml:"info,omitempty"`
	Host    string                              `yaml:"host,omitempty"`
	Tags    []Tag                               `yaml:"tags,omitempty"`
	Servers []Server                            `yaml:"servers,omitempty"`
	Schemes []string                            `yaml:"schemes,omitempty"`
	Paths   map[string]map[string]OperationInfo `yaml:"paths,omitempty"`
}

type Info struct {
	Version     string `yaml:"version,omitempty"`
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type Tag struct {
	Name string `json:"name,omitempty"`
}

type Server struct {
	Url string `json:"url,omitempty"`
}

type OperationInfo struct {
	Tag         string `yaml:"tag,omitempty"`
	OperationId string `yaml:"operationId,omitempty"`
	XrefProduct string `yaml:"x-ref-product,omitempty"`
	XrefApi     string `yaml:"x-ref-api,omitempty"`
}
