package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wselwood/gompcreader"
)

var inputfile = flag.String("in", "", "the minor planet center file to read")
var outputDir = flag.String("out", "", "the output path to write the structure")
var debugMode = flag.Bool("debug", false, "add flag if you want extra debug logging. This has a big performance impact.")
var forceClean = flag.Bool("force", false, "force clean output directory if it contains data")

func outputGrid(dimentions []Dimension, resultTable [][]Grid) {
	for i := 0; i < NumDimentions; i++ {
		for j := 0; j < NumDimentions; j++ {
			path := fmt.Sprintf("%s/%s/%s/", *outputDir, dimentions[i].Name, dimentions[j].Name)
			os.MkdirAll(path, 0777)

			f, err := os.Create(fmt.Sprintf("%s/data.json", path))
			if err != nil {
				log.Fatal("Error opening datafile", err)
			}
			defer f.Close()
			f.WriteString("[")
			first := true

			var table = resultTable[i][j].G

			for x := 0; x < resultTable[i][j].SizeX; x++ {
				for y := 0; y < resultTable[i][j].SizeY; y++ {
					entry := table[x][y]
					if entry.Count > 0 || entry.Special != "" {
						if first {
							first = false
						} else {
							f.WriteString(",\n")
						}

						js, e := json.Marshal(entry)
						if e == nil {
							f.WriteString(fmt.Sprintf("%s", js))
						} else {
							log.Fatal("error json marshal", e)
						}
					}
				}
			}
			f.WriteString("]")
		}
	}
}

func openOrCreateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0666)
}

func pathIsDir(path string) (bool, error) {
	pathStat, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	return pathStat.IsDir(), err
}

func dirContainsFiles(path string) (bool, error) {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return false, err
	}

	return len(files) > 0, nil
}

func main() {

	flag.Parse()

	if *inputfile == "" {
		log.Fatal("No input file provided. Use the -in /path/to/file")
	}

	if *outputDir == "" {
		log.Fatal("No output path provided Use -out /output/path")
	}

	exists, err := pathIsDir(*outputDir)
	if err != nil {
		log.Fatal("Could not check output path existance")
	} else if !exists {
		log.Fatal("Output path does not exist or is not a directory")
	}

	hasFiles, err := dirContainsFiles(*outputDir)
	if err != nil {
		log.Fatal("Could not check dir contence")
	} else if hasFiles {
		if !(*forceClean) {
			log.Fatal("Output directory is not empty. Refusing to overwrite data. Use -force or delete all files and folders in output path manually")
		} else {
			os.RemoveAll(*outputDir)
			os.MkdirAll(*outputDir, 0777)
		}
	}

	// bump up our max file handles, Hopefully will make each channel have a bigger cache
	var rLimit syscall.Rlimit
	err = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		log.Fatal("Error Getting Rlimit ", err)
	}

	if rLimit.Cur < rLimit.Max {
		rLimit.Cur = rLimit.Max
		syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	}

	mpcReader, err := gompcreader.NewMpcReader(*inputfile)
	if err != nil {
		log.Fatal("error creating mpcReader ", err)
	}
	defer mpcReader.Close()

	var dimentions = BuildDimensions()
	var resultTable = BuildResultsGrid(dimentions[:])

	var count int64
	var flushCount int64

	drilldowns := make(map[string]string)

	result, err := mpcReader.ReadEntry()
	for err == nil {

		for i := 0; i < NumDimentions; i++ {
			x := dimentions[i].Extractor.ExtractCell(result)
			if x > 0 {
				for j := 0; j < NumDimentions; j++ {
					y := dimentions[j].Extractor.ExtractCell(result)
					if y > 0 {
						grid := resultTable[i][j].G
						if *debugMode {
							fmt.Printf("i:%2d, j:%2d, x:%3d, y:%3d, c:%d\n", i, j, x, y, count)
						}
						grid[x][y].Count = grid[x][y].Count + 1

						if grid[x][y].X == 0 {
							grid[x][y].X = int(x)
							grid[x][y].Y = int(y)
							grid[x][y].StartX = dimentions[i].Extractor.Extract(result)
							grid[x][y].StartY = dimentions[j].Extractor.Extract(result)
						}
						drillDownPath := fmt.Sprintf("%s/%s/%s/%d/%d.txt", *outputDir, dimentions[i].Name, dimentions[j].Name, x, y)
						v, k := drilldowns[drillDownPath]
						if !k {
							v = "id\n"
						}
						drilldowns[drillDownPath] = v + result.ID + "\n"

					}
				}
			}
		}

		// flush what we have so far to keep our memory useage to a reasonable level.
		if count%10000 == 0 {
			flushCount = flushCount + 1
			for k, v := range drilldowns {
				// only try and create the folders if we know it is new.
				if strings.HasPrefix(v, "id\n") {
					os.MkdirAll(filepath.Dir(k), 0777)
				}

				f, err := openOrCreateFile(k)
				if err != nil {
					log.Fatal(err)
				}
				f.WriteString(strings.TrimRight(v, "\n"))
				f.Close()

				// trim back to just a new line, Don't want to keep putting id in the top of the file.
				drilldowns[k] = "\n"
			}
		}

		result, err = mpcReader.ReadEntry()
		count = count + 1
	}

	if err != nil && err != io.EOF {
		log.Fatal(fmt.Sprintf("error reading line %d\n", count), err)
	}

	for k, v := range drilldowns {
		if v != "\n" {
			os.MkdirAll(filepath.Dir(k), 0777)
			f, err := openOrCreateFile(k)
			if err != nil {
				log.Fatal(err)
			}
			f.WriteString(v)
			f.Close()
		}
		delete(drilldowns, k)
	}

	outputGrid(dimentions[:], resultTable)
	RenderDimensions(*outputDir, dimentions[:])

	fmt.Printf("processed: %d flushes: %d\n", count, flushCount)
}
