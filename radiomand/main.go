package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gin-gonic/gin"
	"github.com/kr/fs"
	"github.com/wtolson/go-taglib"
)

type Playlist struct {
	Name             string    `json:"name"`
	Path             string    `json:"path"`
	CreationDate     time.Time `json:"creation_date"`
	ModificationDate time.Time `json:"modification_date"`
	Status           string    `json:"status"`
	Stats            struct {
		Tracks int `json:"tracks"`
	} `json:"stats"`
	Tracks map[string]*Track `json:"-"`
}

type Track struct {
	Status           string    `json:"status"`
	Title            string    `json:"title"`
	RelPath          string    `json:"relative_path"`
	Path             string    `json:"path"`
	FileName         string    `json:"file_name"`
	FileSize         int64     `json:"file_size"`
	FileModTime      time.Time `json:"file_modification_time"`
	CreationDate     time.Time `json:"creation_date"`
	ModificationDate time.Time `json:"modification_date"`
	Tag              struct {
		Length   time.Duration `json:"length"`
		Title    string        `json:"title"`
		Artist   string        `json:"artist"`
		Album    string        `json:"album"`
		Genre    string        `json:"genre"`
		Bitrate  int           `json:"bitrate"`
		Year     int           `json:"year"`
		Channels int           `json:"channels"`
	} `json:"tag"`
}

type Radio struct {
	Name             string    `json:"name"`
	DefaultPlaylist  *Playlist `json:"default_playlist"`
	CreationDate     time.Time `json:"creation_date"`
	ModificationDate time.Time `json:"modification_date"`
	Stats            struct {
		Playlists int `json:"playlists"`
		Tracks    int `json:"tracks"`
	} `json:"stats"`
	Playlists []*Playlist `json:"-"`
}

var R *Radio

func (t *Track) IsValid() bool {
	return t.Tag.Bitrate >= 64
}

func (p *Playlist) NewLocalTrack(path string) (*Track, error) {
	if track, err := p.GetTrackByPath(path); err == nil {
		return track, nil
	}

	relPath := path
	if strings.Index(path, p.Path) == 0 {
		relPath = path[len(p.Path):]
	}

	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	track := &Track{
		Path:             path,
		RelPath:          relPath,
		FileName:         stat.Name(),
		FileSize:         stat.Size(),
		FileModTime:      stat.ModTime(),
		CreationDate:     time.Now(),
		ModificationDate: time.Now(),
		// Mode:          stat.Mode(),
	}

	file, err := taglib.Read(path)
	if err != nil {
		logrus.Warnf("Failed to read taglib %q: %v", path, err)
	} else {
		defer file.Close()
		track.Tag.Length = file.Length()
		track.Tag.Artist = file.Artist()
		track.Tag.Title = file.Title()
		track.Tag.Album = file.Album()
		track.Tag.Genre = file.Genre()
		track.Tag.Bitrate = file.Bitrate()
		track.Tag.Year = file.Year()
		track.Tag.Channels = file.Channels()
		// fmt.Println(file.Title(), file.Artist(), file.Album(), file.Comment(), file.Genre(), file.Year(), file.Track(), file.Length(), file.Bitrate(), file.Samplerate(), file.Channels())
	}

	p.Tracks[path] = track
	p.Stats.Tracks++
	return track, nil
}

func (p *Playlist) GetTrackByPath(path string) (*Track, error) {
	if track, found := p.Tracks[path]; found {
		return track, nil
	}
	return nil, fmt.Errorf("no such track")
}

func NewRadio(name string) *Radio {
	return &Radio{
		Name:             name,
		Playlists:        make([]*Playlist, 0),
		CreationDate:     time.Now(),
		ModificationDate: time.Now(),
	}
}

func init() {
	R = NewRadio("RadioMan")

	R.NewPlaylist("manual")
	R.NewDirectoryPlaylist("iTunes Music", "~/Music/iTunes/iTunes Media/Music/")
	R.NewDirectoryPlaylist("iTunes Podcasts", "~/Music/iTunes/iTunes Media/Podcasts/")
	dir, err := os.Getwd()
	if err == nil {
		R.NewDirectoryPlaylist("local directory", dir)
	}

	for _, playlistsDir := range []string{"/playlists", path.Join(dir, "playlists")} {
		walker := fs.Walk(playlistsDir)
		for walker.Step() {
			if walker.Path() == playlistsDir {
				continue
			}
			if err := walker.Err(); err != nil {
				logrus.Warnf("walker error: %v", err)
				continue
			}

			var realpath string
			if walker.Stat().IsDir() {
				realpath = walker.Path()
				walker.SkipDir()
			} else {
				realpath, err = filepath.EvalSymlinks(walker.Path())
				if err != nil {
					logrus.Warnf("filepath.EvalSymlinks error for %q: %v", walker.Path(), err)
					continue
				}
			}

			stat, err := os.Stat(realpath)
			if err != nil {
				logrus.Warnf("os.Stat error: %v", err)
				continue
			}
			if stat.IsDir() {
				R.NewDirectoryPlaylist(fmt.Sprintf("playlist: %s", walker.Stat().Name()), realpath)
			}
		}
	}

	playlist, _ := R.GetPlaylistByName("iTunes Music")
	R.DefaultPlaylist = playlist
}

func (r *Radio) NewPlaylist(name string) (*Playlist, error) {
	logrus.Infof("New playlist %q", name)
	playlist := &Playlist{
		Name:             name,
		CreationDate:     time.Now(),
		ModificationDate: time.Now(),
		Tracks:           make(map[string]*Track, 0),
		Status:           "New",
	}
	r.Playlists = append(r.Playlists, playlist)
	r.Stats.Playlists++
	return playlist, nil
}

func (r *Radio) NewDirectoryPlaylist(name string, path string) (*Playlist, error) {
	playlist, err := r.NewPlaylist(name)
	if err != nil {
		return nil, err
	}
	expandedPath, err := expandUser(path)
	if err != nil {
		return nil, err
	}
	playlist.Path = expandedPath
	return playlist, nil
}

func (r *Radio) GetPlaylistByName(name string) (*Playlist, error) {
	for _, playlist := range r.Playlists {
		if playlist.Name == name {
			return playlist, nil
		}
	}
	return nil, fmt.Errorf("no such playlist")
}

func main() {
	router := gin.Default()

	radio := R

	// ping
	router.GET("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})

	// static files
	router.StaticFile("/", "./static/index.html")
	router.Static("/static", "./static")
	router.Static("/bower_components", "./bower_components")

	router.GET("/api/playlists", playlistsEndpoint)
	router.GET("/api/playlists/:name", playlistDetailEndpoint)
	router.PATCH("/api/playlists/:name", playlistUpdateEndpoint)
	router.GET("/api/playlists/:name/tracks", playlistTracksEndpoint)

	router.GET("/api/radios/default", defaultRadioEndpoint)

	router.POST("/api/radios/default/skip-song", radioSkipSongEndpoint)

	router.GET("/api/liquidsoap/getNextSong", getNextSongEndpoint)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	go updatePlaylistsRoutine(radio)

	router.Run(fmt.Sprintf(":%s", port))
}

func getNextSongEndpoint(c *gin.Context) {
	// FIXME: shuffle playlist instead of getting a random track
	// FIXME: do not iterate over a map

	playlist := R.DefaultPlaylist
	track, err := playlist.GetRandomTrack()
	if err == nil {
		c.String(http.StatusOK, track.Path)
		return
	}

	for _, playlist := range R.Playlists {
		track, err := playlist.GetRandomTrack()
		if err != nil {
			continue
		}
		c.String(http.StatusOK, track.Path)
		return
	}

	c.String(http.StatusNotFound, "# cannot get a random song, are your playlists empty ?")
}

func (p *Playlist) GetRandomTrack() (*Track, error) {
	validFiles := 0
	for _, track := range p.Tracks {
		if track.IsValid() {
			validFiles++
		}
	}

	if validFiles == 0 {
		return nil, fmt.Errorf("there is no available track")
	}

	i := rand.Intn(validFiles)
	for _, track := range p.Tracks {
		if !track.IsValid() {
			continue
		}
		if i <= 0 {
			return track, nil
		}
		i--
	}

	return nil, fmt.Errorf("cannot get a random track")
}

func updatePlaylistsRoutine(r *Radio) {
	for {
		tracksSum := 0
		for _, playlist := range r.Playlists {
			if playlist.Path == "" {
				logrus.Debugf("Playlist %q is not dynamic, skipping update", playlist.Name)
				continue
			}

			logrus.Infof("Updating playlist %q", playlist.Name)
			playlist.Status = "Updating"

			walker := fs.Walk(playlist.Path)
			for walker.Step() {
				if err := walker.Err(); err != nil {
					logrus.Warnf("walker error: %v", err)
					continue
				}
				stat := walker.Stat()

				if stat.IsDir() {
					switch stat.Name() {
					case ".git", "bower_components":
						walker.SkipDir()
					}
				} else {
					switch stat.Name() {
					case ".DS_Store":
						continue
					}

					playlist.NewLocalTrack(walker.Path())
				}
			}

			logrus.Infof("Playlist %q updated, %d tracks", playlist.Name, len(playlist.Tracks))
			playlist.Status = "Ready"
			playlist.ModificationDate = time.Now()
			tracksSum += playlist.Stats.Tracks
		}
		r.Stats.Tracks = tracksSum
		time.Sleep(5 * time.Minute)
	}
}

func playlistsEndpoint(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"playlists": R.Playlists,
	})
}

func defaultRadioEndpoint(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"radio": R,
	})
}

func radioSkipSongEndpoint(c *gin.Context) {
	// radio := R

	command := "manager.skip"
	dest := strings.Replace(os.Getenv("LIQUIDSOAP_PORT_2300_TCP"), "tcp://", "", -1)
	conn, _ := net.Dial("tcp", dest)
	fmt.Fprintf(conn, "%s\n", command)
	message, _ := bufio.NewReader(conn).ReadString('\n')
	fmt.Printf("Message from server: %v", message)

	c.JSON(http.StatusOK, gin.H{
		"message": "done",
	})
}

func playlistDetailEndpoint(c *gin.Context) {
	name := c.Param("name")
	playlist, err := R.GetPlaylistByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": err,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"playlist": playlist,
	})
}

func playlistUpdateEndpoint(c *gin.Context) {
	name := c.Param("name")
	playlist, err := R.GetPlaylistByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": err,
		})
		return
	}

	var json struct {
		SetDefault bool `form:"default" json:"default"`
	}

	if err := c.BindJSON(&json); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err,
		})
	}

	if json.SetDefault {
		R.DefaultPlaylist = playlist
	}

	c.JSON(http.StatusOK, gin.H{
		"playlist": playlist,
	})
}

func playlistTracksEndpoint(c *gin.Context) {
	name := c.Param("name")
	playlist, err := R.GetPlaylistByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": err,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tracks": playlist.Tracks,
	})
}