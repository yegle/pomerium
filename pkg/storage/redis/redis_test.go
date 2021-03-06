package redis

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/pomerium/pomerium/pkg/cryptutil"
	"github.com/pomerium/pomerium/pkg/grpc/directory"
)

var db *DB

func cleanup(ctx context.Context, db *DB, t *testing.T) {
	require.NoError(t, db.client.FlushAll(ctx).Err())
}

func tlsConfig(rawURL string, t *testing.T) *tls.Config {
	if !strings.HasPrefix(rawURL, "rediss") {
		return nil
	}
	cert, err := cryptutil.CertificateFromFile("./testdata/tls/redis.crt", "./testdata/tls/redis.key")
	require.NoError(t, err)
	caCertPool := x509.NewCertPool()
	caCert, err := ioutil.ReadFile("./testdata/tls/ca.crt")
	require.NoError(t, err)
	caCertPool.AppendCertsFromPEM(caCert)
	tlsConfig := &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{*cert},
	}
	return tlsConfig
}

func runWithRedisDockerImage(t *testing.T, runOpts *dockertest.RunOptions, withTLS bool, testFunc func(t *testing.T)) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}
	resource, err := pool.RunWithOptions(runOpts)
	if err != nil {
		t.Fatalf("Could not start resource: %s", err)
	}
	resource.Expire(30)

	defer func() {
		if err := pool.Purge(resource); err != nil {
			t.Fatalf("Could not purge resource: %s", err)
		}
	}()

	scheme := "redis"
	if withTLS {
		scheme = "rediss"
	}
	address := fmt.Sprintf(scheme+"://localhost:%s/0", resource.GetPort("6379/tcp"))
	if err := pool.Retry(func() error {
		var err error
		db, err = New(address, WithRecordType("record_type"), WithTLSConfig(tlsConfig(address, t)))
		if err != nil {
			return err
		}
		err = db.client.Ping(context.Background()).Err()
		return err
	}); err != nil {
		t.Fatalf("Could not connect to docker: %s", err)
	}

	testFunc(t)
}

func TestDB(t *testing.T) {
	if os.Getenv("GITHUB_ACTION") != "" && runtime.GOOS == "darwin" {
		t.Skip("Github action can not run docker on MacOS")
	}

	cwd, err := os.Getwd()
	assert.NoError(t, err)

	tlsCmd := []string{
		"--port", "0",
		"--tls-port", "6379",
		"--tls-cert-file", "/tls/redis.crt",
		"--tls-key-file", "/tls/redis.key",
		"--tls-ca-cert-file", "/tls/ca.crt",
	}
	tests := []struct {
		name    string
		withTLS bool
		runOpts *dockertest.RunOptions
	}{
		{"redis", false, &dockertest.RunOptions{Repository: "redis", Tag: "latest"}},
		{"redis TLS", true, &dockertest.RunOptions{Repository: "redis", Tag: "latest", Cmd: tlsCmd, Mounts: []string{cwd + "/testdata/tls:/tls"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runWithRedisDockerImage(t, tc.runOpts, tc.withTLS, testDB)
		})
	}
}

func testDB(t *testing.T) {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	users := []*directory.User{
		{Id: "u1", GroupIds: []string{"test", "admin"}},
		{Id: "u2"},
		{Id: "u3", GroupIds: []string{"test"}},
	}
	ids := []string{"a", "b", "c"}
	id := ids[0]

	t.Run("get missing record", func(t *testing.T) {
		record, err := db.Get(ctx, id)
		assert.Error(t, err)
		assert.Nil(t, record)
	})
	t.Run("get record", func(t *testing.T) {
		data, _ := anypb.New(users[0])
		assert.NoError(t, db.Put(ctx, id, data))
		record, err := db.Get(ctx, id)
		require.NoError(t, err)
		if assert.NotNil(t, record) {
			assert.NotNil(t, record.CreatedAt)
			assert.NotEmpty(t, record.Data)
			assert.Nil(t, record.DeletedAt)
			assert.Equal(t, "a", record.Id)
			assert.NotNil(t, record.ModifiedAt)
			assert.Equal(t, "000000000001", record.Version)
		}
	})
	t.Run("delete record", func(t *testing.T) {
		original, err := db.Get(ctx, id)
		require.NoError(t, err)
		assert.NoError(t, db.Delete(ctx, id))
		record, err := db.Get(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.NotNil(t, record.DeletedAt)
		assert.NotEqual(t, original.GetVersion(), record.GetVersion())
	})
	t.Run("clear deleted", func(t *testing.T) {
		db.ClearDeleted(ctx, time.Now().Add(time.Second))
		record, err := db.Get(ctx, id)
		assert.Error(t, err)
		assert.Nil(t, record)
	})
	t.Run("list", func(t *testing.T) {
		cleanup(ctx, db, t)

		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("%02d", i)
			data := new(anypb.Any)
			assert.NoError(t, db.Put(ctx, id, data))
		}

		records, err := db.List(ctx, "")
		assert.NoError(t, err)
		assert.Len(t, records, 10)
		records, err = db.List(ctx, "000000000005")
		assert.NoError(t, err)
		assert.Len(t, records, 5)
		records, err = db.List(ctx, "000000000010")
		assert.NoError(t, err)
		assert.Len(t, records, 0)
	})
	t.Run("watch", func(t *testing.T) {
		ch := db.Watch(ctx)
		time.Sleep(time.Second)

		go db.Put(ctx, "WATCH", new(anypb.Any))

		select {
		case <-ch:
		case <-time.After(time.Second * 10):
			t.Error("expected watch signal on put")
		}
	})
}
