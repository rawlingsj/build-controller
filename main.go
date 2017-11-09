package main

import (
	"flag"
	"fmt"
	"github.com/golang/glog"
	build "github.com/rawlingsj/build-controller/apis/build/v1"
	buildclient "github.com/rawlingsj/build-controller/client"
	"k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"log"
	"os/exec"
	"time"

	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type Controller struct {
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
}

func NewController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller) *Controller {
	return &Controller{
		informer: informer,
		indexer:  indexer,
		queue:    queue,
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.syncToStdout(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// syncToStdout is the business logic of the controller. In this controller it simply prints
// information about the pod to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) syncToStdout(key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		glog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		// Below we will warm up our cache with a Build, so that we will see a delete for one pod
		fmt.Printf("Build %s does not exist anymore\n", key)
	} else {
		// Note that you also have to check the uid if you have a local controlled resource, which
		// is dependent on the actual instance, to detect that a Pod was recreated with the same name
		fmt.Printf("Sync/Add/Update for Build %s\n", obj.(*build.Build).GetName())
	}
	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		glog.Infof("Error syncing Build %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	glog.Infof("Dropping Build %q out of the queue: %v", key, err)
}

func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	glog.Info("Starting Build controller")

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Info("Stopping Build controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func main() {
	var kubeconfig string
	var master string

	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&master, "master", "", "master url")
	flag.Parse()

	// Create the client config. Use masterURL and kubeconfig if given, otherwise assume in-cluster.
	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		panic(err)
	}

	// make a new config for our extension's API group, using the first config as a baseline
	client, _, err := buildclient.NewClient(config)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// create the pod watcher
	buildListWatcher := cache.NewListWatchFromClient(client, "builds", v1.NamespaceDefault, fields.Everything())

	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the Build key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Build than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(buildListWatcher, &build.Build{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Print("a")
			deployOneShotMaster(clientset)
			//key, err := cache.MetaNamespaceKeyFunc(obj)
			//if err == nil {
			//	queue.Add(key)
			//}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := NewController(queue, indexer, informer)

	// We can now warm up the cache for initial synchronization.
	// Let's suppose that we knew about a BC "mybuild" on our last run, therefore add it to the cache.
	// If this BC is not there anymore, the controller will be notified about the removal after the
	// cache has synchronized.
	indexer.Add(&build.Build{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "mybuild",
			Namespace: v1.NamespaceDefault,
		},
	})

	// Now let's start the controller
	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)

	// Wait forever
	select {}
}

func deployOneShotMaster(client *kubernetes.Clientset) {
	jobClient := client.BatchV1().Jobs(v1.NamespaceDefault)
	_, err := jobClient.Get("jenkins", meta_v1.GetOptions{})
	if err != nil {

		//resp, err := http.Get("https://gist.githubusercontent.com/rawlingsj/707185dfa5aeaf828551b18de827f494/raw/b3d3dfd1e2e48fe34141c28ec625d2ed87fb8af9/gistfile1.txt")
		//if err != nil {
		//	panic(err)
		//}
		//log.Print("4")
		//defer resp.Body.Close()
		//
		//body, err := ioutil.ReadAll(resp.Body)
		//log.Print("6")
		//if err != nil {
		//	panic(err)
		//}
		//log.Print("7")

		e := exec.Command("kubectl", "apply", "-f", "https://gist.githubusercontent.com/rawlingsj/707185dfa5aeaf828551b18de827f494/raw/b3d3dfd1e2e48fe34141c28ec625d2ed87fb8af9/gistfile1.txt")
		err := e.Run()
		if err != nil {
			panic(err)
		}
	}
}
