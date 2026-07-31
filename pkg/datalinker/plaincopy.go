package datalinker

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/datasetversion"
)

type PlainCopyLinker struct {
	objectName string
}

func NewPlainCopyLinker(objectName string) PlainCopyLinker {
	return PlainCopyLinker{objectName: objectName}
}

func (l PlainCopyLinker) collectionNames() (string, string, string) {
	liveCollectionName := fmt.Sprintf("%ss", l.objectName)
	return liveCollectionName, liveCollectionName + "_raw", liveCollectionName + "_staging"
}

func (l PlainCopyLinker) Run() error {
	liveCollectionName, rawCollectionName, stagingCollectionName := l.collectionNames()

	dropCollection(stagingCollectionName)
	defer dropCollection(stagingCollectionName)

	if err := copyCollection(rawCollectionName, stagingCollectionName); err != nil {
		return err
	}
	if err := copyCollection(stagingCollectionName, liveCollectionName); err != nil {
		return err
	}

	return datasetversion.Upsert(context.Background(), ctdf.DatasetVersion{
		Dataset:      datasetversion.LinkerDataset(l.objectName),
		LastModified: time.Now(),
	})
}
