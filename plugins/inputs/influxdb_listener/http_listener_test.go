package http_listener

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yevheniir/telegraf-fork/internal"
	"github.com/yevheniir/telegraf-fork/testutil"

	"github.com/stretchr/testify/require"
)

const (
	testMsg = "cpu_load_short,host=server01 value=12.0 1422568543702900257\n"

	testMsgNoNewline = "cpu_load_short,host=server01 value=12.0 1422568543702900257"

	testMsgs = `cpu_load_short,host=server02 value=12.0 1422568543702900257
cpu_load_short,host=server03 value=12.0 1422568543702900257
cpu_load_short,host=server04 value=12.0 1422568543702900257
cpu_load_short,host=server05 value=12.0 1422568543702900257
cpu_load_short,host=server06 value=12.0 1422568543702900257
`
	badMsg = "blahblahblah: 42\n"

	emptyMsg = ""

	basicUsername = "test-username-please-ignore"
	basicPassword = "super-secure-password!"
)

var (
	pki = testutil.NewPKI("../../../testutil/pki")
)

func newTestHTTPListener() *HTTPListener {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		TimeFunc:       time.Now,
	}
	return listener
}

func newTestHTTPAuthListener() *HTTPListener {
	listener := newTestHTTPListener()
	listener.BasicUsername = basicUsername
	listener.BasicPassword = basicPassword
	return listener
}

func newTestHTTPSListener() *HTTPListener {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		ServerConfig:   *pki.TLSServerConfig(),
		TimeFunc:       time.Now,
	}

	return listener
}

func getHTTPSClient() *http.Client {
	tlsConfig, err := pki.TLSClientConfig().TLSConfig()
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
}

func createURL(listener *HTTPListener, scheme string, path string, rawquery string) string {
	u := url.URL{
		Scheme:   scheme,
		Host:     "localhost:" + strconv.Itoa(listener.Port),
		Path:     path,
		RawQuery: rawquery,
	}
	return u.String()
}

func TestWriteHTTPSNoClientAuth(t *testing.T) {
	listener := newTestHTTPSListener()
	listener.TLSAllowedCACerts = nil

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	cas := x509.NewCertPool()
	cas.AppendCertsFromPEM([]byte(pki.ReadServerCert()))
	noClientAuthClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: cas,
			},
		},
	}

	// post single message to listener
	resp, err := noClientAuthClient.Post(createURL(listener, "https", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)
}

func TestWriteHTTPSWithClientAuth(t *testing.T) {
	listener := newTestHTTPSListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := getHTTPSClient().Post(createURL(listener, "https", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)
}

func TestWriteHTTPBasicAuth(t *testing.T) {
	listener := newTestHTTPAuthListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	client := &http.Client{}

	req, err := http.NewRequest("POST", createURL(listener, "http", "/write", "db=mydb"), bytes.NewBuffer([]byte(testMsg)))
	require.NoError(t, err)
	req.SetBasicAuth(basicUsername, basicPassword)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, http.StatusNoContent, resp.StatusCode)
}

func TestWriteHTTPKeepDatabase(t *testing.T) {
	testMsgWithDB := "cpu_load_short,host=server01,database=wrongdb value=12.0 1422568543702900257\n"

	listener := newTestHTTPListener()
	listener.DatabaseTag = "database"

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(1)
	acc.AssertContainsTaggedFields(t, "cpu_load_short",
		map[string]interface{}{"value": float64(12)},
		map[string]string{"host": "server01", "database": "mydb"},
	)

	// post single message to listener with a database tag in it already. It should be clobbered.
	resp, err = http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsgWithDB)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(1)
	acc.AssertContainsTaggedFields(t, "cpu_load_short",
		map[string]interface{}{"value": float64(12)},
		map[string]string{"host": "server01", "database": "mydb"},
	)

	// post multiple message to listener
	resp, err = http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsgs)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(2)
	hostTags := []string{"server02", "server03",
		"server04", "server05", "server06"}
	for _, hostTag := range hostTags {
		acc.AssertContainsTaggedFields(t, "cpu_load_short",
			map[string]interface{}{"value": float64(12)},
			map[string]string{"host": hostTag, "database": "mydb"},
		)
	}
}

// http listener should add a newline at the end of the buffer if it's not there
func TestWriteHTTPNoNewline(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsgNoNewline)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(1)
	acc.AssertContainsTaggedFields(t, "cpu_load_short",
		map[string]interface{}{"value": float64(12)},
		map[string]string{"host": "server01"},
	)
}

func TestWriteHTTPMaxLineSizeIncrease(t *testing.T) {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		MaxLineSize:    internal.Size{Size: 128 * 1000},
		TimeFunc:       time.Now,
	}

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// Post a gigantic metric to the listener and verify that it writes OK this time:
	resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(hugeMetric)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)
}

func TestWriteHTTPVerySmallMaxBody(t *testing.T) {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		MaxBodySize:    internal.Size{Size: 4096},
		TimeFunc:       time.Now,
	}

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	resp, err := http.Post(createURL(listener, "http", "/write", ""), "", bytes.NewBuffer([]byte(hugeMetric)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 413, resp.StatusCode)
}

func TestWriteHTTPVerySmallMaxLineSize(t *testing.T) {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		MaxLineSize:    internal.Size{Size: 70},
		TimeFunc:       time.Now,
	}

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	resp, err := http.Post(createURL(listener, "http", "/write", ""), "", bytes.NewBuffer([]byte(testMsgs)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	hostTags := []string{"server02", "server03",
		"server04", "server05", "server06"}
	acc.Wait(len(hostTags))
	for _, hostTag := range hostTags {
		acc.AssertContainsTaggedFields(t, "cpu_load_short",
			map[string]interface{}{"value": float64(12)},
			map[string]string{"host": hostTag},
		)
	}
}

func TestWriteHTTPLargeLinesSkipped(t *testing.T) {
	listener := &HTTPListener{
		Log:            testutil.Logger{},
		ServiceAddress: "localhost:0",
		MaxLineSize:    internal.Size{Size: 100},
		TimeFunc:       time.Now,
	}

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	resp, err := http.Post(createURL(listener, "http", "/write", ""), "", bytes.NewBuffer([]byte(hugeMetric+testMsgs)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 400, resp.StatusCode)

	hostTags := []string{"server02", "server03",
		"server04", "server05", "server06"}
	acc.Wait(len(hostTags))
	for _, hostTag := range hostTags {
		acc.AssertContainsTaggedFields(t, "cpu_load_short",
			map[string]interface{}{"value": float64(12)},
			map[string]string{"host": hostTag},
		)
	}
}

// test that writing gzipped data works
func TestWriteHTTPGzippedData(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	data, err := ioutil.ReadFile("./testdata/testmsgs.gz")
	require.NoError(t, err)

	req, err := http.NewRequest("POST", createURL(listener, "http", "/write", ""), bytes.NewBuffer(data))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.EqualValues(t, 204, resp.StatusCode)

	hostTags := []string{"server02", "server03",
		"server04", "server05", "server06"}
	acc.Wait(len(hostTags))
	for _, hostTag := range hostTags {
		acc.AssertContainsTaggedFields(t, "cpu_load_short",
			map[string]interface{}{"value": float64(12)},
			map[string]string{"host": hostTag},
		)
	}
}

// writes 25,000 metrics to the listener with 10 different writers
func TestWriteHTTPHighTraffic(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Skipping due to hang on darwin")
	}
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post many messages to listener
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(innerwg *sync.WaitGroup) {
			defer innerwg.Done()
			for i := 0; i < 500; i++ {
				resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(testMsgs)))
				require.NoError(t, err)
				resp.Body.Close()
				require.EqualValues(t, 204, resp.StatusCode)
			}
		}(&wg)
	}

	wg.Wait()
	listener.Gather(acc)

	acc.Wait(25000)
	require.Equal(t, int64(25000), int64(acc.NMetrics()))
}

func TestReceive404ForInvalidEndpoint(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := http.Post(createURL(listener, "http", "/foobar", ""), "", bytes.NewBuffer([]byte(testMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 404, resp.StatusCode)
}

func TestWriteHTTPInvalid(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(badMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 400, resp.StatusCode)
}

func TestWriteHTTPEmpty(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post single message to listener
	resp, err := http.Post(createURL(listener, "http", "/write", "db=mydb"), "", bytes.NewBuffer([]byte(emptyMsg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)
}

func TestQueryAndPingHTTP(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	// post query to listener
	resp, err := http.Post(
		createURL(listener, "http", "/query", "db=&q=CREATE+DATABASE+IF+NOT+EXISTS+%22mydb%22"), "", nil)
	require.NoError(t, err)
	require.EqualValues(t, 200, resp.StatusCode)

	// post ping to listener
	resp, err = http.Post(createURL(listener, "http", "/ping", ""), "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)
}

func TestWriteWithPrecision(t *testing.T) {
	listener := newTestHTTPListener()

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	msg := "xyzzy value=42 1422568543\n"
	resp, err := http.Post(
		createURL(listener, "http", "/write", "precision=s"), "", bytes.NewBuffer([]byte(msg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(1)
	require.Equal(t, 1, len(acc.Metrics))
	require.Equal(t, time.Unix(0, 1422568543000000000), acc.Metrics[0].Time)
}

func TestWriteWithPrecisionNoTimestamp(t *testing.T) {
	listener := newTestHTTPListener()
	listener.TimeFunc = func() time.Time {
		return time.Unix(42, 123456789)
	}

	acc := &testutil.Accumulator{}
	require.NoError(t, listener.Start(acc))
	defer listener.Stop()

	msg := "xyzzy value=42\n"
	resp, err := http.Post(
		createURL(listener, "http", "/write", "precision=s"), "", bytes.NewBuffer([]byte(msg)))
	require.NoError(t, err)
	resp.Body.Close()
	require.EqualValues(t, 204, resp.StatusCode)

	acc.Wait(1)
	require.Equal(t, 1, len(acc.Metrics))
	require.Equal(t, time.Unix(42, 0), acc.Metrics[0].Time)
}

const hugeMetric = `super_long_metric,foo=bar clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i,clients=1i,connected_slaves=0i,evicted_keys=0i,expired_keys=0i,instantaneous_ops_per_sec=0i,keyspace_hitrate=0,keyspace_hits=0i,keyspace_misses=2i,latest_fork_usec=0i,master_repl_offset=0i,mem_fragmentation_ratio=3.58,pubsub_channels=0i,pubsub_patterns=0i,rdb_changes_since_last_save=0i,repl_backlog_active=0i,repl_backlog_histlen=0i,repl_backlog_size=1048576i,sync_full=0i,sync_partial_err=0i,sync_partial_ok=0i,total_commands_processed=4i,total_connections_received=2i,uptime=869i,used_cpu_sys=0.07,used_cpu_sys_children=0,used_cpu_user=0.1,used_cpu_user_children=0,used_memory=502048i,used_memory_lua=33792i,used_memory_peak=501128i,used_memory_rss=1798144i
`
