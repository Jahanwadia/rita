package uconnproxy

import (
	"runtime"

	"github.com/activecm/rita/resources"
	"github.com/activecm/rita/util"
	"github.com/globalsign/mgo"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
)

type repo struct {
	res *resources.Resources
}

// NewMongoRepository create new repository
func NewMongoRepository(res *resources.Resources) Repository {
	return &repo{
		res: res,
	}
}

func (r *repo) CreateIndexes() error {
	session := r.res.DB.Session.Copy()
	defer session.Close()

	// set collection name
	collectionName := r.res.Config.T.Structure.UniqueConnProxyTable

	// check if collection already exists
	names, _ := session.DB(r.res.DB.GetSelectedDB()).CollectionNames()

	// if collection exists, we don't need to do anything else
	for _, name := range names {
		if name == collectionName {
			return nil
		}
	}

	indexes := []mgo.Index{
		{Key: []string{"src", "fqdn", "src_network_uuid"}, Unique: true},
		{Key: []string{"fqdn"}},
		{Key: []string{"src", "src_network_uuid"}},
		{Key: []string{"$dat.count"}},
	}

	// create collection
	err := r.res.DB.CreateCollection(collectionName, indexes)
	if err != nil {
		return err
	}

	return nil
}

// Upsert loops through every uconnproxy entry
func (r *repo) Upsert(uconnProxyMap map[string]*Input) {
	// Create the workers
	writerWorker := newWriter(r.res.Config.T.Structure.UniqueConnProxyTable, r.res.DB, r.res.Config, r.res.Log)

	analyzerWorker := newAnalyzer(
		r.res.Config.S.Rolling.CurrentChunk,
		int64(r.res.Config.S.Strobe.ConnectionLimit),
		r.res.DB,
		r.res.Config,
		writerWorker.collect,
		writerWorker.close,
	)

	// kick off the threaded goroutines
	for i := 0; i < util.Max(1, runtime.NumCPU()/2); i++ {
		analyzerWorker.start()
		writerWorker.start()
	}

	// progress bar for troubleshooting
	p := mpb.New(mpb.WithWidth(20))
	bar := p.AddBar(int64(len(uconnProxyMap)),
		mpb.PrependDecorators(
			decor.Name("\t[-] Uconn Proxy Analysis:", decor.WC{W: 30, C: decor.DidentRight}),
			decor.CountersNoUnit(" %d / %d ", decor.WCSyncWidth),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	// loop over map entries
	for _, entry := range uconnProxyMap {
		analyzerWorker.collect(entry)
		bar.IncrBy(1)
	}
	p.Wait()

	// start the closing cascade (this will also close the other channels)
	analyzerWorker.close()
}