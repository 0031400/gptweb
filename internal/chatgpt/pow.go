package chatgpt

import (
	"bytes"
	"crypto/rand"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Proof-of-work 生成逻辑参考自 chatgpt2api 的逆向实现。
// 服务端要求 proof_token 里的浏览器指纹与 prepare 请求里的 p 一致，
// 因此两者必须由同一份 config 生成。
type POW struct{}

var processStarted = time.Now()

var navigatorKeys = []string{
	"registerProtocolHandler−function registerProtocolHandler() { [native code] }", "storage−[object StorageManager]", "locks−[object LockManager]", "appCodeName−Mozilla", "permissions−[object Permissions]", "share−function share() { [native code] }", "webdriver−false", "managed−[object NavigatorManagedData]", "canShare−function canShare() { [native code] }", "vendor−Google Inc.", "mediaDevices−[object MediaDevices]", "vibrate−function vibrate() { [native code] }", "storageBuckets−[object StorageBucketManager]", "mediaCapabilities−[object MediaCapabilities]", "cookieEnabled−true", "virtualKeyboard−[object VirtualKeyboard]", "product−Gecko", "presentation−[object Presentation]", "onLine−true", "mimeTypes−[object MimeTypeArray]", "credentials−[object CredentialsContainer]", "serviceWorker−[object ServiceWorkerContainer]", "keyboard−[object Keyboard]", "gpu−[object GPU]", "doNotTrack", "serial−[object Serial]", "pdfViewerEnabled−true", "language−zh-CN", "geolocation−[object Geolocation]", "userAgentData−[object NavigatorUAData]", "getUserMedia−function getUserMedia() { [native code] }", "sendBeacon−function sendBeacon() { [native code] }", "hardwareConcurrency−32", "windowControlsOverlay−[object WindowControlsOverlay]",
}

var windowKeys = []string{
	"0", "window", "self", "document", "name", "location", "customElements", "history", "navigation", "innerWidth", "innerHeight", "scrollX", "scrollY", "visualViewport", "screenX", "screenY", "outerWidth", "outerHeight", "devicePixelRatio", "screen", "chrome", "navigator", "onresize", "performance", "crypto", "indexedDB", "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa", "fetch", "matchMedia", "postMessage", "queueMicrotask", "requestAnimationFrame", "setInterval", "setTimeout", "caches", "__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
}

var documentKeys = []string{"__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"}

func (p POW) BuildPToken(userAgent, dataBuild string) (string, []any) {
	config := p.buildConfig(userAgent, dataBuild)
	data, _ := json.Marshal(config)
	return "gAAAAAC" + base64.StdEncoding.EncodeToString(data), config
}

func (p POW) BuildProofToken(config []any, seed, difficulty, userAgent, dataBuild string) (string, error) {
	target, err := hex.DecodeString(difficulty)
	if err != nil {
		return "", err
	}
	if len(target) == 0 {
		target = []byte{0x0f, 0xff, 0xff}
	}
	seedBytes := []byte(seed)
	for nonce := 0; nonce < 2_000_000; nonce++ {
		config[3], config[9] = nonce, nonce>>1
		data, _ := json.Marshal(config)
		encoded := base64.StdEncoding.EncodeToString(data)
		digest := sha3.Sum512(append(seedBytes, []byte(encoded)...))
		if bytes.Compare(digest[:len(target)], target) <= 0 {
			return "gAAAAAB" + encoded, nil
		}
	}
	return "", errors.New("计算工作量证明失败")
}

func (POW) buildConfig(userAgent, dataBuild string) []any {
	cores := []int{8, 16, 24, 32}
	screens := []int{3000, 2340, 4000, 6000}
	document := []string{"__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"}
	scriptSource := "https://chatgpt.com/backend-api/sentinel/sdk.js"
	elapsed := float64(time.Since(processStarted).Nanoseconds()) / 1e6
	return []any{
		screens[randomIndex(len(screens))],
		time.Now().In(time.FixedZone("EST", -5*3600)).Format("Mon Jan 02 2006 15:04:05") + " GMT-0500 (Eastern Standard Time)",
		4294705152, 1, userAgent, scriptSource, dataBuild, "en-US", "en-US,es-US,en,es",
		randomFloat(), navigatorKeys[randomIndex(len(navigatorKeys))], document[randomIndex(len(document))],
		windowKeys[randomIndex(len(windowKeys))], elapsed, uuid.NewString(), "", cores[randomIndex(len(cores))],
		float64(time.Now().UnixMilli()) - elapsed, 0, 0, 0, 0, 0, 0, 0,
	}
}

func randomIndex(limit int) int {
	value, _ := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	return int(value.Int64())
}

func randomFloat() float64 {
	value, _ := rand.Int(rand.Reader, big.NewInt(1<<53))
	return float64(value.Int64()) / float64(1<<53)
}
