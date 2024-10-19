package datalinker

type Linker interface {
	GetBaseCollectionName() string
	Run()
}
