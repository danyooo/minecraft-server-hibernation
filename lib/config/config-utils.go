package config

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"msh/lib/errco"
	"msh/lib/model"
	"msh/lib/utility"
)

// InWhitelist checks if the playerName or clientAddress is in config whitelist
func (c *Configuration) InWhitelist(params ...string) *errco.Error {
	// check if whitelist is enabled
	// if empty then it is not enabled and no checks are needed
	if len(c.Msh.Whitelist) == 0 {
		errco.Logln(errco.LVL_3, "whitelist not enabled")
		return nil
	}

	errco.Logln(errco.LVL_3, "checking whitelist for: %s", strings.Join(params, ", "))

	// check if playerName or clientAddress are in whitelist
	for _, p := range params {
		if utility.SliceContain(p, c.Msh.Whitelist) {
			errco.Logln(errco.LVL_3, "playerName or clientAddress is whitelisted!")
			return nil
		}
	}

	// playerName or clientAddress not found in whitelist
	errco.Logln(errco.LVL_3, "playerName or clientAddress is not whitelisted!")
	return errco.NewErr(errco.ERROR_PLAYER_NOT_IN_WHITELIST, errco.LVL_1, "InWhitelist", "playerName or clientAddress is not whitelisted")
}

// loadIcon tries to load user specified server icon (base-64 encoded and compressed).
// The default icon is loaded by default
func (c *Configuration) loadIcon() *errco.Error {
	// set default server icon
	ServerIcon = defaultServerIcon

	// get the path of the user specified server icon
	userIconPaths := []string{}
	userIconPaths = append(userIconPaths, filepath.Join(c.Server.Folder, "server-icon-frozen.png"))
	userIconPaths = append(userIconPaths, filepath.Join(c.Server.Folder, "server-icon-frozen.jpg"))

	for _, uip := range userIconPaths {
		// check if user specified icon exists
		_, err := os.Stat(uip)
		if os.IsNotExist(err) {
			// user specified server icon not found
			continue
		}

		// open file
		f, err := os.Open(uip)
		if err != nil {
			errco.LogMshErr(errco.NewErr(errco.ERROR_ICON_LOAD, errco.LVL_3, "loadIcon", err.Error()))
			continue
		}
		defer f.Close()

		// read file data
		// it's important to read all file data and store it in a variable that can be read multiple times with a io.Reader.
		// using f *os.File directly in Decode(r io.Reader) results in f *os.File readable only once.
		fdata, err := ioutil.ReadAll(f)
		if err != nil {
			errco.LogMshErr(errco.NewErr(errco.ERROR_ICON_LOAD, errco.LVL_3, "loadIcon", err.Error()))
			continue
		}

		// decode image (try different formats)
		var img image.Image
		if img, err = png.Decode(bytes.NewReader(fdata)); err == nil {
		} else if img, err = jpeg.Decode(bytes.NewReader(fdata)); err == nil {
		} else {
			errco.LogMshErr(errco.NewErr(errco.ERROR_ICON_LOAD, errco.LVL_3, "loadIcon", "data format invalid: "+uip+" ("+err.Error()+")"))
			continue
		}

		// scale image to 64x64
		scaImg, d := utility.ScaleImg(img, image.Rect(0, 0, 64, 64))
		errco.Logln(errco.LVL_3, "scaled %s to 64x64. (%v ms)", uip, d.Milliseconds())

		// encode image to png
		enc, buff := &png.Encoder{CompressionLevel: -3}, &bytes.Buffer{} // -3: best compression
		err = enc.Encode(buff, scaImg)
		if err != nil {
			errco.LogMshErr(errco.NewErr(errco.ERROR_ICON_LOAD, errco.LVL_3, "loadIcon", err.Error()))
			continue
		}

		// load user specified server icon as base64 encoded string
		ServerIcon = base64.RawStdEncoding.EncodeToString(buff.Bytes())

		// as soon as a good image is loaded, break and return
		break
	}

	return nil
}

// loadIpPorts reads server.properties server file and loads correct ports to global variables
func (c *Configuration) loadIpPorts() *errco.Error {
	// ListenHost remains the same
	ListenPort = c.Msh.ListenPort
	// TargetHost remains the same
	// TargetPort is extracted from server.properties

	data, err := ioutil.ReadFile(filepath.Join(c.Server.Folder, "server.properties"))
	if err != nil {
		return errco.NewErr(errco.ERROR_CONFIG_LOAD, errco.LVL_1, "loadIpPorts", err.Error())
	}

	TargetPortStr, errMsh := utility.StrBetween(strings.ReplaceAll(string(data), "\r", ""), "server-port=", "\n")
	if errMsh != nil {
		return errMsh.AddTrace("loadIpPorts")
	}

	TargetPort, err = strconv.Atoi(TargetPortStr)
	if err != nil {
		return errco.NewErr(errco.ERROR_CONVERSION, errco.LVL_3, "loadIpPorts", err.Error())
	}

	if TargetPort == c.Msh.ListenPort {
		return errco.NewErr(errco.ERROR_CONFIG_LOAD, errco.LVL_1, "loadIpPorts", "TargetPort and ListenPort appear to be the same, please change one of them")
	}

	return nil
}

// getVersionInfo reads version.json from the server JAR file
// and returns minecraft server version and protocol.
// In case of error "", 0, *errco.Error are returned.
func (c *Configuration) getVersionInfo() (string, int, *errco.Error) {
	reader, err := zip.OpenReader(filepath.Join(c.Server.Folder, c.Server.FileName))
	if err != nil {
		return "", 0, errco.NewErr(errco.ERROR_VERSION_LOAD, errco.LVL_3, "getVersionInfo", err.Error())
	}
	defer reader.Close()

	for _, file := range reader.File {
		// search for version.json file
		if file.Name != "version.json" {
			continue
		}

		f, err := file.Open()
		if err != nil {
			return "", 0, errco.NewErr(errco.ERROR_VERSION_LOAD, errco.LVL_3, "getVersionInfo", err.Error())
		}
		defer f.Close()

		versionsBytes, err := ioutil.ReadAll(f)
		if err != nil {
			return "", 0, errco.NewErr(errco.ERROR_VERSION_LOAD, errco.LVL_3, "getVersionInfo", err.Error())
		}

		var info model.VersionInfo
		err = json.Unmarshal(versionsBytes, &info)
		if err != nil {
			return "", 0, errco.NewErr(errco.ERROR_VERSION_LOAD, errco.LVL_3, "getVersionInfo", err.Error())
		}

		return info.Version, info.Protocol, nil
	}

	return "", 0, errco.NewErr(errco.ERROR_VERSION_LOAD, errco.LVL_3, "getVersionInfo", "minecraft server version and protocol could not be extracted from version.json")
}

// assignMshID assigns a mshid to config.
// Config mshid is kept if valid, otherwise a new random one is generated
func (c *Configuration) assignMshID() {
	if len(c.Msh.ID) == 40 {
		// use mshid already present in config
		errco.LogMshErr(errco.NewErr(errco.ERROR_CONFIG_CHECK, errco.LVL_3, "assignMshID", "mshid in config is valid, keeping it"))
	} else {
		// generate random mshid
		key := make([]byte, 64)
		_, _ = rand.Read(key)
		hasher := sha1.New()
		hasher.Write(key)
		c.Msh.ID = hex.EncodeToString(hasher.Sum(nil))
		configDefaultSave = true
		errco.LogMshErr(errco.NewErr(errco.ERROR_CONFIG_CHECK, errco.LVL_3, "assignMshID", "mshid in config is not valid, new one is: "+c.Msh.ID))
	}
}
