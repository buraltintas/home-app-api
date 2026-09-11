// build-locations turns two public datasets into the single compressed table this
// product ships its location picker with. It runs on a developer's machine, not in
// production: the output file is committed, and the API reads it from the binary.
//
// Sources, both open:
//
//	tr_il_ilce_mahalle_koordinat.csv  kratlg/turkey-geolocation-dataset (MIT) --
//	    every neighbourhood in Turkey with a latitude and longitude.
//	provinces.json                    turkiyeapi.dev -- province centroids and the
//	    population of every province and district.
//
// Neither is official, and the coordinates are community-compiled, so this tool does not
// trust a single point. A district's position is the median of its neighbourhoods, which
// no single bad row can move, and any neighbourhood sitting implausibly far from that
// median is dropped rather than published. The counts it prints are the evidence that the
// data is sane; read them before committing the output.
package main

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/textnorm"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const (
	defaultCSV       = "https://raw.githubusercontent.com/kratlg/turkey-geolocation-dataset/HEAD/tr_il_ilce_mahalle_koordinat.csv"
	defaultProvinces = "https://turkiyeapi.dev/api/v1/provinces"
	// Turkey's bounding box with a little slack. A coordinate outside it is not a
	// Turkish neighbourhood, whatever the row says.
	minLat, maxLat = 35.5, 42.5
	minLon, maxLon = 25.4, 45.1
	// How far a neighbourhood may sit from its own district's median before we treat the
	// row as broken. Turkey's largest district is about 120 km across, so 80 km from the
	// middle is generous for a real place and unmistakable for a mistyped coordinate.
	maxStrayMeters = 80000
)

type province struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Population  int    `json:"population"`
	Coordinates struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"coordinates"`
	Districts []struct {
		Name       string `json:"name"`
		Population int    `json:"population"`
	} `json:"districts"`
}

type record struct {
	id, kind, name, key, parentID, province, district string
	lat, lon                                          float64
	population                                        int
}

type node struct {
	sourceID string
	name     string
	lats     []float64
	lons     []float64
}

func main() {
	csvSource := flag.String("csv", defaultCSV, "neighbourhood CSV: URL or local path")
	provinceSource := flag.String("provinces", defaultProvinces, "province JSON: URL or local path")
	out := flag.String("out", "internal/location/data/tr_locations.csv.gz", "output file")
	flag.Parse()

	provinces := loadProvinces(*provinceSource)
	log.Printf("provinces: %d", len(provinces))

	rows := loadNeighbourhoods(*csvSource)
	log.Printf("neighbourhood rows: %d", len(rows))

	records := assemble(provinces, rows)
	write(*out, records)
}

func open(source string) io.ReadCloser {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		client := &http.Client{Timeout: 3 * time.Minute}
		request, e := http.NewRequest(http.MethodGet, source, nil)
		if e != nil {
			log.Fatal(e)
		}
		// Identify the fetcher. A dataset maintainer reading their logs should be able to
		// see who took a copy and why.
		request.Header.Set("User-Agent", "BosaGezmeBot/1.0 (+https://bosagezme.com/bot)")
		response, e := client.Do(request)
		if e != nil {
			log.Fatal(e)
		}
		if response.StatusCode != http.StatusOK {
			log.Fatalf("%s: %s", source, response.Status)
		}
		return response.Body
	}
	file, e := os.Open(source)
	if e != nil {
		log.Fatal(e)
	}
	return file
}

func loadProvinces(source string) []province {
	body := open(source)
	defer body.Close()
	var payload struct {
		Data []province `json:"data"`
	}
	if e := json.NewDecoder(body).Decode(&payload); e != nil {
		log.Fatal(e)
	}
	if len(payload.Data) != 81 {
		log.Fatalf("expected 81 provinces, got %d", len(payload.Data))
	}
	return payload.Data
}

type rawRow struct {
	provinceID, districtID, neighbourhoodID string
	province, district, neighbourhood       string
	lat, lon                                float64
}

func loadNeighbourhoods(source string) []rawRow {
	body := open(source)
	defer body.Close()
	reader := csv.NewReader(body)
	reader.FieldsPerRecord = -1
	header, e := reader.Read()
	if e != nil {
		log.Fatal(e)
	}
	// The file is published with a byte-order mark; without stripping it the first column
	// name is not "il_id" and every lookup below misses.
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	index := map[string]int{}
	for i, name := range header {
		index[strings.TrimSpace(name)] = i
	}
	for _, needed := range []string{"il_id", "il_adi", "ilce_id", "ilce_adi", "mahalle_id", "mahalle_adi", "enlem", "boylam"} {
		if _, ok := index[needed]; !ok {
			log.Fatalf("column %q missing from %s", needed, source)
		}
	}
	var rows []rawRow
	var unparsable int
	for {
		line, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			unparsable++
			continue
		}
		lat, latErr := strconv.ParseFloat(strings.TrimSpace(line[index["enlem"]]), 64)
		lon, lonErr := strconv.ParseFloat(strings.TrimSpace(line[index["boylam"]]), 64)
		if latErr != nil || lonErr != nil || lat < minLat || lat > maxLat || lon < minLon || lon > maxLon {
			unparsable++
			continue
		}
		rows = append(rows, rawRow{
			provinceID:      strings.TrimSpace(line[index["il_id"]]),
			districtID:      strings.TrimSpace(line[index["ilce_id"]]),
			neighbourhoodID: strings.TrimSpace(line[index["mahalle_id"]]),
			province:        strings.TrimSpace(line[index["il_adi"]]),
			district:        strings.TrimSpace(line[index["ilce_adi"]]),
			neighbourhood:   strings.TrimSpace(line[index["mahalle_adi"]]),
			lat:             lat,
			lon:             lon,
		})
	}
	log.Printf("rows rejected as unusable: %d", unparsable)
	return rows
}

func assemble(provinces []province, rows []rawRow) []record {
	// Turkish casing, not Go's default: the registry publishes "BALIKESİR", and lowering
	// its dotless I with the ordinary rules produces "Balikesir" -- a different word.
	// cases.Title(Turkish) lowers the tail itself, so the input is handed over untouched.
	titler := cases.Title(language.Turkish)
	title := func(raw string) string { return titler.String(strings.TrimSpace(raw)) }

	provincePopulation := map[string]int{}
	provinceCoords := map[string][2]float64{}
	districtPopulation := map[string]int{}
	for _, p := range provinces {
		key := textnorm.Key(p.Name)
		provincePopulation[key] = p.Population
		provinceCoords[key] = [2]float64{p.Coordinates.Latitude, p.Coordinates.Longitude}
		for _, d := range p.Districts {
			districtPopulation[key+"|"+textnorm.Key(d.Name)] = d.Population
		}
	}

	// Group first, decide second: a district's position cannot be known until all of its
	// neighbourhoods have been seen.
	districts := map[string]*node{}
	order := []string{}
	for _, row := range rows {
		key := row.provinceID + "|" + row.districtID
		group, ok := districts[key]
		if !ok {
			group = &node{sourceID: row.districtID, name: row.district}
			districts[key] = group
			order = append(order, key)
		}
		group.lats = append(group.lats, row.lat)
		group.lons = append(group.lons, row.lon)
	}

	provinceNames := map[string]string{}
	for _, row := range rows {
		provinceNames[row.provinceID] = row.province
	}

	districtCentre := map[string][2]float64{}
	for key, group := range districts {
		districtCentre[key] = [2]float64{median(group.lats), median(group.lons)}
	}

	var records []record
	seen := map[string]bool{}

	// Provinces.
	provinceIDs := make([]string, 0, len(provinceNames))
	for id := range provinceNames {
		provinceIDs = append(provinceIDs, id)
	}
	sort.Strings(provinceIDs)
	for _, id := range provinceIDs {
		name := title(provinceNames[id])
		key := textnorm.Key(name)
		coords, ok := provinceCoords[key]
		if !ok {
			log.Printf("province %q has no published centroid; falling back to its districts", name)
			var lats, lons []float64
			for groupKey, group := range districts {
				if strings.HasPrefix(groupKey, id+"|") {
					lats = append(lats, median(group.lats))
					lons = append(lons, median(group.lons))
				}
			}
			coords = [2]float64{median(lats), median(lons)}
		}
		records = append(records, record{
			id: "il:" + id, kind: "il", name: name, key: key,
			province: name, lat: coords[0], lon: coords[1], population: provincePopulation[key],
		})
		seen["il:"+id] = true
	}

	// Districts.
	sort.Strings(order)
	for _, groupKey := range order {
		group := districts[groupKey]
		provinceID := strings.SplitN(groupKey, "|", 2)[0]
		provinceName := title(provinceNames[provinceID])
		name := title(group.name)
		centre := districtCentre[groupKey]
		records = append(records, record{
			id: "ilce:" + provinceID + "-" + group.sourceID, kind: "ilce", name: name, key: textnorm.Key(name),
			parentID: "il:" + provinceID, province: provinceName, district: name,
			lat: centre[0], lon: centre[1],
			population: districtPopulation[textnorm.Key(provinceName)+"|"+textnorm.Key(name)],
		})
		seen["ilce:"+provinceID+"-"+group.sourceID] = true
	}

	// Neighbourhoods.
	var stray, duplicate int
	for _, row := range rows {
		groupKey := row.provinceID + "|" + row.districtID
		centre := districtCentre[groupKey]
		if metresBetween(row.lat, row.lon, centre[0], centre[1]) > maxStrayMeters {
			stray++
			continue
		}
		name := cleanNeighbourhood(title(row.neighbourhood))
		if name == "" {
			continue
		}
		id := "mah:" + row.provinceID + "-" + row.neighbourhoodID
		if seen[id] {
			duplicate++
			continue
		}
		seen[id] = true
		records = append(records, record{
			id: id, kind: "mahalle", name: name, key: textnorm.Key(name),
			parentID: "ilce:" + row.provinceID + "-" + row.districtID,
			province: title(row.province), district: title(row.district),
			lat: row.lat, lon: row.lon,
		})
	}
	log.Printf("neighbourhoods dropped as strays: %d, as duplicate ids: %d", stray, duplicate)
	return records
}

// cleanNeighbourhood removes the registry's own suffixes. "Akören Mah.(Merkez)" is the
// way an address form prints it; "Akören" is the way a person types it.
func cleanNeighbourhood(name string) string {
	name = strings.TrimSpace(name)
	if open := strings.Index(name, "("); open >= 0 {
		name = strings.TrimSpace(name[:open])
	}
	for _, suffix := range []string{" Mah.", " Mah", " Mh.", " Mh", " Mahallesi", " Köyü", " Köy", " Belde"} {
		if strings.HasSuffix(name, suffix) {
			name = strings.TrimSpace(strings.TrimSuffix(name, suffix))
			break
		}
	}
	return strings.TrimSpace(strings.Trim(name, "-,./"))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func metresBetween(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000
	toRadians := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRadians(lat2 - lat1)
	dLon := toRadians(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRadians(lat1))*math.Cos(toRadians(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Min(1, math.Sqrt(a)))
}

func write(path string, records []record) {
	if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
		log.Fatal(e)
	}
	file, e := os.Create(path)
	if e != nil {
		log.Fatal(e)
	}
	defer file.Close()
	zip, e := gzip.NewWriterLevel(file, gzip.BestCompression)
	if e != nil {
		log.Fatal(e)
	}
	writer := csv.NewWriter(zip)
	if e := writer.Write([]string{"id", "kind", "name", "search_key", "parent_id", "province_name", "district_name", "latitude", "longitude", "population"}); e != nil {
		log.Fatal(e)
	}
	counts := map[string]int{}
	for _, r := range records {
		counts[r.kind]++
		if e := writer.Write([]string{
			r.id, r.kind, r.name, r.key, r.parentID, r.province, r.district,
			strconv.FormatFloat(r.lat, 'f', 6, 64), strconv.FormatFloat(r.lon, 'f', 6, 64),
			strconv.Itoa(r.population),
		}); e != nil {
			log.Fatal(e)
		}
	}
	writer.Flush()
	if e := writer.Error(); e != nil {
		log.Fatal(e)
	}
	if e := zip.Close(); e != nil {
		log.Fatal(e)
	}
	info, e := file.Stat()
	if e != nil {
		log.Fatal(e)
	}
	fmt.Printf("wrote %s: %d il, %d ilçe, %d mahalle (%d rows, %.1f MB compressed)\n",
		path, counts["il"], counts["ilce"], counts["mahalle"], len(records), float64(info.Size())/(1024*1024))
}
