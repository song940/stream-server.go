package conf

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/nacl/secretbox"

	"github.com/bluenviron/mediamtx/internal/logger"
)

func writeTempFile(byts []byte) (string, error) {
	tmpf, err := os.CreateTemp(os.TempDir(), "rtsp-")
	if err != nil {
		return "", err
	}
	defer tmpf.Close()

	_, err = tmpf.Write(byts)
	if err != nil {
		return "", err
	}

	return tmpf.Name(), nil
}

func TestConfFromFile(t *testing.T) {
	func() {
		tmpf, err := writeTempFile([]byte("logLevel: debug\n" +
			"paths:\n" +
			"  cam1:\n" +
			"    runOnDemandStartTimeout: 5s\n"))
		require.NoError(t, err)
		defer os.Remove(tmpf)

		conf, confPath, err := Load(tmpf, nil)
		require.NoError(t, err)
		require.Equal(t, tmpf, confPath)

		require.Equal(t, LogLevel(logger.Debug), conf.LogLevel)

		pa, ok := conf.Paths["cam1"]
		require.Equal(t, true, ok)
		require.Equal(t, &Path{
			Name:                       "cam1",
			Source:                     "publisher",
			SourceOnDemandStartTimeout: 10 * StringDuration(time.Second),
			SourceOnDemandCloseAfter:   10 * StringDuration(time.Second),
			Playback:                   true,
			RecordPath:                 "./recordings/%path/%Y-%m-%d_%H-%M-%S-%f",
			RecordFormat:               RecordFormatFMP4,
			RecordPartDuration:         100000000,
			RecordSegmentDuration:      3600000000000,
			RecordDeleteAfter:          86400000000000,
			OverridePublisher:          true,
			RPICameraWidth:             1920,
			RPICameraHeight:            1080,
			RPICameraContrast:          1,
			RPICameraSaturation:        1,
			RPICameraSharpness:         1,
			RPICameraExposure:          "normal",
			RPICameraAWB:               "auto",
			RPICameraDenoise:           "off",
			RPICameraMetering:          "centre",
			RPICameraFPS:               30,
			RPICameraIDRPeriod:         60,
			RPICameraBitrate:           1000000,
			RPICameraProfile:           "main",
			RPICameraLevel:             "4.1",
			RPICameraAfMode:            "continuous",
			RPICameraAfRange:           "normal",
			RPICameraAfSpeed:           "normal",
			RPICameraTextOverlay:       "%Y-%m-%d %H:%M:%S - MediaMTX",
			RunOnDemandStartTimeout:    5 * StringDuration(time.Second),
			RunOnDemandCloseAfter:      10 * StringDuration(time.Second),
		}, pa)
	}()

	func() {
		tmpf, err := writeTempFile([]byte(``))
		require.NoError(t, err)
		defer os.Remove(tmpf)

		_, _, err = Load(tmpf, nil)
		require.NoError(t, err)
	}()

	func() {
		tmpf, err := writeTempFile([]byte(`paths:`))
		require.NoError(t, err)
		defer os.Remove(tmpf)

		_, _, err = Load(tmpf, nil)
		require.NoError(t, err)
	}()

	func() {
		tmpf, err := writeTempFile([]byte(
			"paths:\n" +
				"  mypath:\n"))
		require.NoError(t, err)
		defer os.Remove(tmpf)

		_, _, err = Load(tmpf, nil)
		require.NoError(t, err)
	}()
}

func TestConfFromFileAndEnv(t *testing.T) {
	// global parameter
	t.Setenv("RTSP_PROTOCOLS", "tcp")

	// path parameter
	t.Setenv("MTX_PATHS_CAM1_SOURCE", "rtsp://testing")

	// deprecated global parameter
	t.Setenv("MTX_RTMPDISABLE", "yes")

	// deprecated path parameter
	t.Setenv("MTX_PATHS_CAM2_DISABLEPUBLISHEROVERRIDE", "yes")

	tmpf, err := writeTempFile([]byte("{}"))
	require.NoError(t, err)
	defer os.Remove(tmpf)

	conf, confPath, err := Load(tmpf, nil)
	require.NoError(t, err)
	require.Equal(t, tmpf, confPath)

	require.Equal(t, Protocols{Protocol(gortsplib.TransportTCP): {}}, conf.Protocols)
	require.Equal(t, false, conf.RTMP)

	pa, ok := conf.Paths["cam1"]
	require.Equal(t, true, ok)
	require.Equal(t, "rtsp://testing", pa.Source)

	pa, ok = conf.Paths["cam2"]
	require.Equal(t, true, ok)
	require.Equal(t, false, pa.OverridePublisher)
}

func TestConfFromEnvOnly(t *testing.T) {
	t.Setenv("MTX_PATHS_CAM1_SOURCE", "rtsp://testing")

	conf, confPath, err := Load("", nil)
	require.NoError(t, err)
	require.Equal(t, "", confPath)

	pa, ok := conf.Paths["cam1"]
	require.Equal(t, true, ok)
	require.Equal(t, "rtsp://testing", pa.Source)
}

func TestConfEncryption(t *testing.T) {
	key := "testing123testin"
	plaintext := "paths:\n" +
		"  path1:\n" +
		"  path2:\n"

	encryptedConf := func() string {
		var secretKey [32]byte
		copy(secretKey[:], key)

		var nonce [24]byte
		_, err := io.ReadFull(rand.Reader, nonce[:])
		require.NoError(t, err)

		encrypted := secretbox.Seal(nonce[:], []byte(plaintext), &nonce, &secretKey)
		return base64.StdEncoding.EncodeToString(encrypted)
	}()

	t.Setenv("RTSP_CONFKEY", key)

	tmpf, err := writeTempFile([]byte(encryptedConf))
	require.NoError(t, err)
	defer os.Remove(tmpf)

	conf, confPath, err := Load(tmpf, nil)
	require.NoError(t, err)
	require.Equal(t, tmpf, confPath)

	_, ok := conf.Paths["path1"]
	require.Equal(t, true, ok)

	_, ok = conf.Paths["path2"]
	require.Equal(t, true, ok)
}

func TestConfErrors(t *testing.T) {
	for _, ca := range []struct {
		name string
		conf string
		err  string
	}{
		{
			"non existent parameter 1",
			`invalid: param`,
			"json: unknown field \"invalid\"",
		},
		{
			"invalid writeQueueSize",
			"writeQueueSize: 1001\n",
			"'writeQueueSize' must be a power of two",
		},
		{
			"invalid udpMaxPayloadSize",
			"udpMaxPayloadSize: 5000\n",
			"'udpMaxPayloadSize' must be less than 1472",
		},
		{
			"invalid externalAuthenticationURL 1",
			"externalAuthenticationURL: testing\n",
			"'externalAuthenticationURL' must be a HTTP URL",
		},
		{
			"invalid externalAuthenticationURL 2",
			"externalAuthenticationURL: http://myurl\n" +
				"authMethods: [digest]\n",
			"'externalAuthenticationURL' can't be used when 'digest' is in authMethods",
		},
		{
			"invalid strict encryption 1",
			"encryption: strict\n" +
				"protocols: [udp]\n",
			"strict encryption can't be used with the UDP transport protocol",
		},
		{
			"invalid strict encryption 2",
			"encryption: strict\n" +
				"protocols: [multicast]\n",
			"strict encryption can't be used with the UDP-multicast transport protocol",
		},
		{
			"invalid ICE server",
			"webrtcICEServers: [testing]\n",
			"invalid ICE server: 'testing'",
		},
		{
			"non existent parameter 2",
			"paths:\n" +
				"  mypath:\n" +
				"    invalid: parameter\n",
			"json: unknown field \"invalid\"",
		},
		{
			"invalid path name",
			"paths:\n" +
				"  '':\n" +
				"    source: publisher\n",
			"invalid path name '': cannot be empty",
		},
		{
			"double raspberry pi camera",
			"paths:\n" +
				"  cam1:\n" +
				"    source: rpiCamera\n" +
				"  cam2:\n" +
				"    source: rpiCamera\n",
			"'rpiCamera' with same camera ID 0 is used as source in two paths, 'cam2' and 'cam1'",
		},
		{
			"invalid srt publish passphrase",
			"paths:\n" +
				"  mypath:\n" +
				"    srtPublishPassphrase: a\n",
			`invalid 'srtPublishPassphrase': must be between 10 and 79 characters`,
		},
		{
			"invalid srt read passphrase",
			"paths:\n" +
				"  mypath:\n" +
				"    srtReadPassphrase: a\n",
			`invalid 'readRTPassphrase': must be between 10 and 79 characters`,
		},
		{
			"all_others aliases",
			"paths:\n" +
				"  all:\n" +
				"  all_others:\n",
			`all_others, all and '~^.*$' are aliases`,
		},
		{
			"all_others aliases",
			"paths:\n" +
				"  all_others:\n" +
				"  ~^.*$:\n",
			`all_others, all and '~^.*$' are aliases`,
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			tmpf, err := writeTempFile([]byte(ca.conf))
			require.NoError(t, err)
			defer os.Remove(tmpf)

			_, _, err = Load(tmpf, nil)
			require.EqualError(t, err, ca.err)
		})
	}
}

func TestSampleConfFile(t *testing.T) {
	func() {
		conf1, confPath1, err := Load("../../mediamtx.yml", nil)
		require.NoError(t, err)
		require.Equal(t, "../../mediamtx.yml", confPath1)
		conf1.Paths = make(map[string]*Path)
		conf1.OptionalPaths = nil

		conf2, confPath2, err := Load("", nil)
		require.NoError(t, err)
		require.Equal(t, "", confPath2)

		require.Equal(t, conf1, conf2)
	}()

	func() {
		conf1, confPath1, err := Load("../../mediamtx.yml", nil)
		require.NoError(t, err)
		require.Equal(t, "../../mediamtx.yml", confPath1)

		tmpf, err := writeTempFile([]byte("paths:\n  all_others:"))
		require.NoError(t, err)
		defer os.Remove(tmpf)

		conf2, confPath2, err := Load(tmpf, nil)
		require.NoError(t, err)
		require.Equal(t, tmpf, confPath2)

		require.Equal(t, conf1.Paths, conf2.Paths)
	}()
}
