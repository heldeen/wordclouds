package wordclouds

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
)

type wordCount struct {
	word  string
	count int
}

type word2D struct {
	wordCount
	x           float64
	y           float64
	height      float64
	boundingBox *Box
}

// Wordcloud object. Create one with NewWordcloud and use Draw() to get the image
type Wordcloud struct {
	wordList        map[string]int
	sortedWordList  []wordCount
	grid            *spatialHashMap
	dc              *gg.Context
	overlapCount    int
	words2D         []*word2D
	availableColors []color.Color
	randomPlacement bool
	width           float64
	height          float64
	opts            Options
	circles         map[float64]*circle
	fonts           map[float64]font.Face
	radii           []float64
}

type Options struct {
	FontMaxSize     int
	FontMinSize     int
	RandomPlacement bool
	FontFile        string
	Colors          []color.Color
	BackgroundColor color.Color
	Width           int
	Height          int
	Mask            []*Box
	Debug           bool
}

var defaultOptions = Options{
	FontMaxSize:     500,
	FontMinSize:     10,
	RandomPlacement: false,
	FontFile:        "",
	Colors:          []color.Color{color.RGBA{}},
	BackgroundColor: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	Width:           2048,
	Height:          2048,
	Mask:            make([]*Box, 0),
	Debug:           false,
}

type Option func(*Options)

// Path to font file
func FontFile(path string) Option {
	return func(options *Options) {
		options.FontFile = path
	}
}

// Output file background color
func BackgroundColor(color color.Color) Option {
	return func(options *Options) {
		options.BackgroundColor = color
	}
}

// Colors to use for the words
func Colors(colors []color.Color) Option {
	return func(options *Options) {
		options.Colors = colors
	}
}

// Max font size
func FontMaxSize(max int) Option {
	return func(options *Options) {
		options.FontMaxSize = max
	}
}

// Min font size
func FontMinSize(min int) Option {
	return func(options *Options) {
		options.FontMinSize = min
	}
}

// A list of bounding boxes where words can not be placed.
// See Mask
func MaskBoxes(mask []*Box) Option {
	return func(options *Options) {
		options.Mask = mask
	}
}

func Width(w int) Option {
	return func(options *Options) {
		options.Width = w
	}
}

func Height(h int) Option {
	return func(options *Options) {
		options.Height = h
	}
}

// Place words randomly
func RandomPlacement(do bool) Option {
	return func(options *Options) {
		options.RandomPlacement = do
	}
}

// Draw bounding boxes around words
func Debug() Option {
	return func(options *Options) {
		options.Debug = true
	}
}

// Initialize a wordcloud based on a map of word frequency.
func NewWordcloud(wordList map[string]int, options ...Option) *Wordcloud {
	opts := defaultOptions
	for _, opt := range options {
		opt(&opts)
	}

	sortedWordList := make([]wordCount, 0, len(wordList))
	for word, count := range wordList {
		sortedWordList = append(sortedWordList, wordCount{word: strings.Trim(word, " "), count: count})
	}
	sort.Slice(sortedWordList, func(i, j int) bool {
		return sortedWordList[i].count > sortedWordList[j].count
	})

	dc := gg.NewContext(opts.Width, opts.Height)
	dc.SetColor(opts.BackgroundColor)
	dc.Clear()
	dc.SetRGB(0, 0, 0)
	grid := newSpatialHashMap(float64(opts.Width), float64(opts.Height), opts.Height/10)

	for _, b := range opts.Mask {
		if opts.Debug {
			dc.DrawRectangle(b.x(), b.y(), b.w(), b.h())
			dc.Stroke()
		}
		grid.Add(b)
	}

	radius := 1.0
	maxRadius := math.Sqrt(float64(opts.Width*opts.Width + opts.Height*opts.Height))
	circles := make(map[float64]*circle)
	radii := make([]float64, 0)
	for radius < maxRadius {
		circles[radius] = newCircle(float64(opts.Width/2), float64(opts.Height/2), radius, 512)
		radii = append(radii, radius)
		radius = radius + 5.0
	}

	rand.Seed(time.Now().UnixNano())

	return &Wordcloud{
		wordList:        wordList,
		sortedWordList:  sortedWordList,
		grid:            grid,
		dc:              dc,
		words2D:         make([]*word2D, 0),
		randomPlacement: opts.RandomPlacement,
		width:           float64(opts.Width),
		height:          float64(opts.Height),
		opts:            opts,
		circles:         circles,
		fonts:           make(map[float64]font.Face),
		radii:           radii,
	}
}

func (w *Wordcloud) getPreciseBoundingBoxes(b *Box) []*Box {
	res := make([]*Box, 0)
	step := 5

	defColor := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for i := int(math.Floor(b.Left)); i < int(b.Right); i = i + step {
		for j := int(b.Bottom); j < int(b.Top); j = j + step {
			if w.dc.Image().At(i, j) != defColor {
				res = append(res, &Box{
					float64(j+step) + 5,
					float64(i) - 5,
					float64(i+step) + 5,
					float64(j) - 5,
				})
			}
		}
	}
	return res
}

func (w *Wordcloud) setFont(size float64) {
	_, ok := w.fonts[size]

	if !ok {
		f, err := gg.LoadFontFace(w.opts.FontFile, size)
		if err != nil {
			panic(err)
		}
		w.fonts[size] = f
	}

	w.dc.SetFontFace(w.fonts[size])
}

func (w *Wordcloud) Place(wc wordCount) bool {
	c := w.opts.Colors[rand.Intn(len(w.opts.Colors))]
	w.dc.SetColor(c)

	size := float64(w.opts.FontMaxSize) * (float64(wc.count) / float64(w.sortedWordList[0].count))

	if size < float64(w.opts.FontMinSize) {
		size = float64(w.opts.FontMinSize)
	}
	w.setFont(size)
	width, height := w.dc.MeasureString(wc.word)

	width += 5
	height += 5
	x, y, space := w.nextPos(width, height)
	if !space {
		return false
	}
	w.dc.DrawStringAnchored(wc.word, x, y, 0.5, 0.5)

	box := &Box{
		y + height/2 + 0.3*height,
		x - width/2,
		x + width/2,
		math.Max(y-height/2, 0),
	}
	if height > 40 {
		preciseBoxes := w.getPreciseBoundingBoxes(box)
		for _, pb := range preciseBoxes {
			w.grid.Add(pb)
			if w.opts.Debug {
				w.dc.DrawRectangle(pb.x(), pb.y(), pb.w(), pb.h())
				w.dc.Stroke()
			}
		}
	} else {
		w.grid.Add(box)
	}
	return true
}

// Draw tries to place words one by one, starting with the ones with the highest counts
func (w *Wordcloud) Draw() image.Image {
	consecutiveMisses := 0
	for _, wc := range w.sortedWordList {
		success := w.Place(wc)
		if !success {
			consecutiveMisses++
			if consecutiveMisses > 10 {
				return w.dc.Image()
			}
			continue
		}
		consecutiveMisses = 0
	}
	return w.dc.Image()
}

func (w *Wordcloud) nextRandom(width float64, height float64) (x float64, y float64, space bool) {
	tries := 0
	searching := true
	var box Box
	for searching && tries < 5000000 {
		tries++
		x, y = float64(rand.Intn(w.dc.Width())), float64(rand.Intn(w.dc.Height()))
		// Is that position available?
		box.Top = y + height/2
		box.Left = x - width/2
		box.Right = x + width/2
		box.Bottom = y - height/2

		if !box.fits(w.width, w.height) {
			continue
		}
		colliding, _ := w.grid.TestCollision(&box, func(a *Box, b *Box) bool {
			return a.overlaps(b)
		})

		if !colliding {
			space = true
			searching = false
			return
		}
	}
	return
}

// Data sent to placement workers
type workerData struct {
	radius    float64
	positions []point
	width     float64
	height    float64
}

// Results sent from placement workers
type res struct {
	radius float64
	x      float64
	y      float64
	failed bool
}

// Multithreaded word placement
func (w *Wordcloud) nextPos(width float64, height float64) (x float64, y float64, space bool) {
	if w.randomPlacement {
		return w.nextRandom(width, height)
	}

	space = false

	x, y = w.width, w.height

	stopSendingCh := make(chan struct{}, 1)
	aggCh := make(chan res, 100)
	workCh := make(chan workerData, runtime.NumCPU())
	results := make(map[float64]res)
	done := make(map[float64]bool)
	stopChannels := make([]chan struct{}, 0)
	wg := sync.WaitGroup{}

	// Start workers that will test each one "circle" of positions
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		stopCh := make(chan struct{}, 1)
		go func(ch chan struct{}, i int) {
			defer wg.Done()
			for {
				select {
				// Receive data
				case d, ok := <-workCh:
					if !ok {
						return
					}
					// Test the positions and post results on aggCh
					aggCh <- w.testRadius(d.radius, d.positions, d.width, d.height)
				case <-ch:
					// Stop signal
					return
				}
			}
		}(stopCh, i)
		stopChannels = append(stopChannels, stopCh)
	}

	// Post positions to test to worker channel
	go func() {
		for _, r := range w.radii {
			select {
			case <-stopSendingCh:
				// Stop sending data immediately if a position has already been found
				close(workCh)
				return
			default:
			}
			c := w.circles[r]
			workCh <- workerData{
				radius:    r,
				positions: c.positions(),
				width:     width,
				height:    height,
			}
		}
		// Close channel after all positions have been sent
		close(workCh)
	}()

	defer func() {
		// Stop data sending
		stopSendingCh <- struct{}{}
		// Tell the worker goroutines to stop
		for _, c := range stopChannels {
			c <- struct{}{}
		}
		// Purge res channel in case some workers are still sending data
		go func() {
			for range aggCh {
			}
		}()

		// Wait for all goroutines to stop. We want to wait for them so that no thread is accessing internal data structs
		// such as the spatial hashmap
		wg.Wait()
	}()

	// Finally, aggregate the results coming from workers
	for d := range aggCh {
		results[d.radius] = d
		done[d.radius] = true
		//check if we need to continue
		failed := true
		// Example: if we know that there's a successful placement at r=10 but have not received results for r=5,
		// we need to wait as there might be a closer successful position
		for _, r := range w.radii {
			if !done[r] {
				// Some positions are not done. They might be successful
				failed = false
				break
			}
			// We have the successful placement with the lowest radius
			if !results[r].failed {
				return results[r].x, results[r].y, true
			}
		}

		// We tried it all but could not place the word
		if failed {
			return
		}

	}
	return
}

// test a series of points on a circle and returns as soon as there's a match
func (w *Wordcloud) testRadius(radius float64, points []point, width float64, height float64) res {
	var box Box
	var x, y float64

	for _, p := range points {
		y = p.y
		x = p.x

		// Is that position available?
		box.Top = y + height/2
		box.Left = x - width/2
		box.Right = x + width/2
		box.Bottom = y - height/2

		if !box.fits(w.width, w.height) {
			continue
		}
		colliding, _ := w.grid.TestCollision(&box, func(a *Box, b *Box) bool {
			return a.overlaps(b)
		})

		if !colliding {
			return res{
				x:      x,
				y:      y,
				failed: false,
				radius: radius,
			}
		}
	}
	return res{
		x:      x,
		y:      y,
		failed: true,
		radius: radius,
	}
}
