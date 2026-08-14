// Package coverage normaliza los manifests públicos de cobertura móvil
// en una estructura común para el backend y el frontend.
package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Catalog agrupa todos los proveedores de cobertura disponibles.
type Catalog struct {
	GeneratedAt   time.Time            `json:"generated_at"`
	Providers     []Provider           `json:"providers"`
	movistarIndex map[string][]Overlay `json:"-"`
}

// Provider describe un proveedor y sus tecnologías normalizadas.
type Provider struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	SourceType   string        `json:"source_type"`
	PublicPage   string        `json:"public_page,omitempty"`
	MapPage      string        `json:"map_page,omitempty"`
	UpdatedAt    string        `json:"updated_at,omitempty"`
	Notes        []string      `json:"notes,omitempty"`
	Stats        ProviderStats `json:"stats"`
	Technologies []Technology  `json:"technologies"`
}

// ProviderStats resume el tamaño de la fuente.
type ProviderStats struct {
	Departments    int `json:"departments,omitempty"`
	Cities         int `json:"cities,omitempty"`
	Municipalities int `json:"municipalities,omitempty"`
	Localities     int `json:"localities,omitempty"`
	Admins         int `json:"admins,omitempty"`
	Layers         int `json:"layers,omitempty"`
	TileMatrixSets int `json:"tile_matrix_sets,omitempty"`
	Overlays       int `json:"overlays,omitempty"`
}

// Technology describe una capa o tecnología específica.
type Technology struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	RenderType           string        `json:"render_type"`
	Renderable           bool          `json:"renderable"`
	SourceURLs           []string      `json:"source_urls,omitempty"`
	TileURLTemplates     []string      `json:"tile_url_templates,omitempty"`
	OverlayEndpoint      string        `json:"overlay_endpoint,omitempty"`
	OverlayCount         int           `json:"overlay_count,omitempty"`
	Departments          int           `json:"departments,omitempty"`
	Cities               int           `json:"cities,omitempty"`
	Localities           int           `json:"localities,omitempty"`
	ActiveLocalityCities int           `json:"active_locality_cities,omitempty"`
	TileMatrixSets       []string      `json:"tile_matrix_sets,omitempty"`
	BBox                 *BBox         `json:"bbox,omitempty"`
	Legend               []LegendEntry `json:"legend,omitempty"`
	Notes                []string      `json:"notes,omitempty"`
}

// LegendEntry reexpone la leyenda nativa cuando existe.
type LegendEntry struct {
	Descripcion        string `json:"descripcion,omitempty"`
	DescripcionLeyenda string `json:"descripcion_leyenda,omitempty"`
	IDTecno            string `json:"id_tecno,omitempty"`
	Nombre             string `json:"nombre,omitempty"`
	Rango              string `json:"rango,omitempty"`
}

// BBox es una caja geográfica en lon/lat.
type BBox struct {
	West  float64 `json:"west"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	North float64 `json:"north"`
}

// Overlay representa una imagen georreferenciada de Movistar.
type Overlay struct {
	URL  string `json:"url"`
	BBox BBox   `json:"bbox"`
}

// LoadCatalog lee los manifests locales y devuelve un catálogo unificado.
// Si falta alguna fuente, se devuelve el catálogo parcial junto con el error.
func LoadCatalog(baseDir string) (Catalog, error) {
	baseDir = resolveBaseDir(baseDir)
	cat := Catalog{
		GeneratedAt:   time.Now().UTC(),
		Providers:     make([]Provider, 0, 4),
		movistarIndex: map[string][]Overlay{},
	}

	var errs []string

	if provider, index, err := loadMovistar(filepath.Join(baseDir, "movistar_cobertura")); err != nil {
		errs = append(errs, "movistar: "+err.Error())
	} else {
		cat.Providers = append(cat.Providers, provider)
		for techID, overlays := range index {
			cat.movistarIndex[normalizeID("movistar:"+techID)] = overlays
		}
	}

	if provider, err := loadClaro(filepath.Join(baseDir, "claro_cobertura")); err != nil {
		errs = append(errs, "claro: "+err.Error())
	} else {
		cat.Providers = append(cat.Providers, provider)
	}

	if provider, err := loadTigo(filepath.Join(baseDir, "tigo_cobertura")); err != nil {
		errs = append(errs, "tigo: "+err.Error())
	} else {
		cat.Providers = append(cat.Providers, provider)
	}

	if provider, err := loadWOM(filepath.Join(baseDir, "wom_cobertura")); err != nil {
		errs = append(errs, "wom: "+err.Error())
	} else {
		cat.Providers = append(cat.Providers, provider)
	}

	if len(cat.Providers) == 0 && len(errs) > 0 {
		return cat, errors.New(strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		return cat, errors.New(strings.Join(errs, "; "))
	}
	return cat, nil
}

// ProviderByID busca un proveedor por ID.
func (c Catalog) ProviderByID(id string) (Provider, bool) {
	id = normalizeID(id)
	for _, p := range c.Providers {
		if normalizeID(p.ID) == id {
			return p, true
		}
	}
	return Provider{}, false
}

// TechnologyByID busca una tecnología dentro de un proveedor.
func (p Provider) TechnologyByID(id string) (Technology, bool) {
	id = normalizeID(id)
	for _, tech := range p.Technologies {
		if normalizeID(tech.ID) == id {
			return tech, true
		}
	}
	return Technology{}, false
}

// MovistarOverlays filtra los overlays de Movistar por tecnología y bbox.
func (c Catalog) MovistarOverlays(technology string, bbox BBox) []Overlay {
	overlays := c.movistarIndex[normalizeID("movistar:"+technology)]
	if len(overlays) == 0 {
		return nil
	}
	out := make([]Overlay, 0, len(overlays))
	for _, ov := range overlays {
		if ov.BBox.Intersects(bbox) {
			out = append(out, ov)
		}
	}
	return out
}

func loadMovistar(root string) (Provider, map[string][]Overlay, error) {
	var manifest movistarManifest
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		return Provider{}, nil, err
	}

	provider := Provider{
		ID:         "movistar",
		Name:       "Movistar",
		SourceType: "kml-image-overlays",
		PublicPage: manifest.Source.PublicPage,
		MapPage:    manifest.Source.EmbedPage,
		UpdatedAt:  manifest.GeneratedAt,
		Notes: []string{
			"Las capas se renderizan como image overlays georreferenciados.",
			"Se consulta solo el viewport visible para mantener el mapa liviano.",
		},
	}

	var overlaysIndex = map[string][]Overlay{}
	var maxDepartments, maxCities, maxLocalities, totalOverlays int

	for _, tech := range manifest.Technologies {
		overlayPath := filepath.Join(root, tech.KMLManifestPath)
		overlays, err := loadMovistarOverlays(overlayPath)
		if err != nil {
			return Provider{}, nil, fmt.Errorf("%s: %w", tech.ID, err)
		}
		maxDepartments = max(maxDepartments, tech.Departments)
		maxCities = max(maxCities, tech.Cities)
		maxLocalities = max(maxLocalities, tech.Localities)
		totalOverlays += len(overlays)

		sourceURLs := append([]string(nil), tech.CallKMLURLs...)
		t := Technology{
			ID:                   tech.ID,
			Name:                 tech.Name,
			RenderType:           "image-overlays",
			Renderable:           true,
			SourceURLs:           sourceURLs,
			OverlayEndpoint:      "/coverage/overlays?provider=movistar&technology=" + url.QueryEscape(tech.ID),
			OverlayCount:         len(overlays),
			Departments:          tech.Departments,
			Cities:               tech.Cities,
			Localities:           tech.Localities,
			ActiveLocalityCities: tech.ActiveLocalityCities,
			Legend:               convertLegend(tech.Legend),
			Notes: []string{
				"Overlay PNGs servidos por el KML nacional.",
			},
		}
		provider.Technologies = append(provider.Technologies, t)
		overlaysIndex[normalizeID(tech.ID)] = overlays
	}

	provider.Stats = ProviderStats{
		Departments: maxDepartments,
		Cities:      maxCities,
		Localities:  maxLocalities,
		Overlays:    totalOverlays,
	}
	return provider, overlaysIndex, nil
}

func loadMovistarOverlays(path string) ([]Overlay, error) {
	var manifest movistarKMLManifest
	if err := readJSON(path, &manifest); err != nil {
		return nil, err
	}

	out := make([]Overlay, 0, 1024)
	for _, root := range manifest.Roots {
		for _, item := range root.GroundOverlays {
			bbox, err := bboxFromStrings(item.BBox.West, item.BBox.South, item.BBox.East, item.BBox.North)
			if err != nil {
				continue
			}
			out = append(out, Overlay{
				URL:  item.URL,
				BBox: bbox,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BBox.West == out[j].BBox.West {
			return out[i].BBox.South < out[j].BBox.South
		}
		return out[i].BBox.West < out[j].BBox.West
	})
	return out, nil
}

func loadClaro(root string) (Provider, error) {
	var manifest claroManifest
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		return Provider{}, err
	}

	depts := map[string]struct{}{}
	muns := map[string]struct{}{}
	for _, loc := range manifest.Localities {
		if loc.DepartmentID != "" {
			depts[loc.DepartmentID] = struct{}{}
		}
		if loc.MunicipalityID != "" {
			muns[loc.MunicipalityID] = struct{}{}
		}
	}

	provider := Provider{
		ID:         "claro",
		Name:       "Claro",
		SourceType: "xyz-tiles",
		PublicPage: "https://www.claro.com.co/personas/servicios/servicios-moviles/cobertura/",
		MapPage:    "https://minisitiosclaro.claro.com.co/MapasDeCobertura",
		UpdatedAt:  manifest.GeneratedAt,
		Notes: []string{
			"Mapa basado en mosaicos PNG directos; no expone KML.",
		},
		Stats: ProviderStats{
			Departments:    len(depts),
			Municipalities: len(muns),
			Localities:     len(manifest.Localities),
		},
	}

	order := []string{"GSM", "UMTS", "LTE", "5G"}
	for _, techID := range order {
		base, ok := manifest.BaseURLs[claroBaseKey(techID)]
		if !ok || strings.TrimSpace(base) == "" {
			continue
		}
		t := Technology{
			ID:         techID,
			Name:       claroTechName(techID),
			RenderType: "xyz-tiles",
			Renderable: true,
			TileURLTemplates: []string{
				joinURL(base, "Z{z}/{y}/{x}.png"),
			},
			Notes: []string{
				"Mosaicos PNG directos del mapa embebido de Claro.",
			},
		}
		provider.Technologies = append(provider.Technologies, t)
	}

	return provider, nil
}

func loadTigo(root string) (Provider, error) {
	var manifest tigoManifest
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		return Provider{}, err
	}

	provider := Provider{
		ID:         "tigo",
		Name:       "Tigo",
		SourceType: "xyz-tiles",
		PublicPage: manifest.Source.PublicPage,
		MapPage:    manifest.Source.MapPage,
		UpdatedAt:  manifest.PageUpdate,
		Notes: append([]string{
			"Mosaicos PNG directos; la capa ciudad/ciudades se usa a mayor zoom.",
		}, manifest.Notes...),
		Stats: ProviderStats{
			Departments: len(manifest.Departments),
			Cities:      len(manifest.Cities),
			Admins:      len(manifest.Admins),
		},
	}

	for _, tech := range manifest.Technologies {
		templates := normalizeTileTemplates(manifest.Source.MapPage, tech.TileURLTemplates)
		t := Technology{
			ID:               tech.ID,
			Name:             tigoTechName(tech.ID),
			RenderType:       "xyz-tiles",
			Renderable:       true,
			TileURLTemplates: templates,
			Notes: []string{
				"El primer template es el mosaico general; el segundo, si existe, es la capa ciudad/ciudades.",
			},
		}
		provider.Technologies = append(provider.Technologies, t)
	}

	return provider, nil
}

func loadWOM(root string) (Provider, error) {
	var manifest womManifest
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		return Provider{}, err
	}

	provider := Provider{
		ID:         "wom",
		Name:       "WOM",
		SourceType: "wmts",
		PublicPage: manifest.Source.PublicPage,
		MapPage:    manifest.Source.PublicPage,
		UpdatedAt:  manifest.PageUpdate,
		Notes: append([]string{
			"WOM publica por WMTS/GeoServer, no como mosaicos XYZ ni KML.",
			"El catálogo queda listo para un adaptador WMTS si se decide agregarlo después.",
		}, manifest.Notes...),
		Stats: ProviderStats{
			Layers:         manifest.Summary.CoverageLayers + manifest.Summary.HelperLayers,
			TileMatrixSets: manifest.Summary.TileMatrixSets,
		},
	}

	for _, layer := range manifest.Layers {
		var tileTemplates []string
		var tileMatrixSets []string
		var bbox *BBox
		for _, u := range layer.ResourceURLs {
			if u.ResourceType == "tile" && strings.Contains(strings.ToLower(u.Format), "png") {
				tileTemplates = append(tileTemplates, u.Template)
			}
		}
		if len(layer.TileMatrixSets) > 0 {
			tileMatrixSets = append(tileMatrixSets, layer.TileMatrixSets...)
		}
		if layer.BBox != nil {
			lonLat := strings.Fields(layer.BBox.Lower)
			upperLonLat := strings.Fields(layer.BBox.Upper)
			if len(lonLat) == 2 && len(upperLonLat) == 2 {
				if parsed, err := bboxFromStrings(lonLat[0], lonLat[1], upperLonLat[0], upperLonLat[1]); err == nil {
					bbox = &parsed
				}
			}
		}
		t := Technology{
			ID:               layer.Identifier,
			Name:             layer.Title,
			RenderType:       "wmts",
			Renderable:       false,
			TileURLTemplates: normalizeAbsoluteTemplates(tileTemplates),
			TileMatrixSets:   tileMatrixSets,
			BBox:             bbox,
			Notes: []string{
				"WMTS catalogado, pero aún no adaptado a la grilla XYZ del visor.",
			},
		}
		provider.Technologies = append(provider.Technologies, t)
	}

	return provider, nil
}

func normalizeTileTemplates(base string, templates []string) []string {
	out := make([]string, 0, len(templates))
	for _, tpl := range templates {
		out = append(out, normalizeLeafletTemplate(joinURL(base, tpl)))
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci := strings.Contains(out[i], "/ciudades/")
		cj := strings.Contains(out[j], "/ciudades/")
		if ci != cj {
			return !ci && cj
		}
		return out[i] < out[j]
	})
	return out
}

func normalizeAbsoluteTemplates(templates []string) []string {
	out := make([]string, 0, len(templates))
	for _, tpl := range templates {
		out = append(out, normalizeLeafletTemplate(tpl))
	}
	return out
}

func normalizeLeafletTemplate(s string) string {
	return strings.ReplaceAll(s, "{zoom}", "{z}")
}

func joinURL(base, ref string) string {
	base = strings.TrimRight(base, "/")
	ref = strings.TrimLeft(ref, "/")
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if base == "" {
		return ref
	}
	return base + "/" + ref
}

func claroBaseKey(techID string) string {
	switch strings.ToUpper(strings.TrimSpace(techID)) {
	case "GSM":
		return "ulrMapGSM"
	case "UMTS":
		return "ulrMapUMTS"
	case "LTE":
		return "ulrMapLTE"
	case "5G":
		return "ulrMap5G"
	default:
		return ""
	}
}

func claroTechName(techID string) string {
	switch strings.ToUpper(strings.TrimSpace(techID)) {
	case "GSM":
		return "GSM 2G"
	case "UMTS":
		return "UMTS 3G"
	case "LTE":
		return "LTE 4G"
	case "5G":
		return "5G"
	default:
		return techID
	}
}

func tigoTechName(techID string) string {
	switch strings.ToUpper(strings.TrimSpace(techID)) {
	case "3G":
		return "3G"
	case "4G":
		return "4G"
	case "5G":
		return "5G"
	default:
		return techID
	}
}

func convertLegend(in []movistarLegendEntry) []LegendEntry {
	out := make([]LegendEntry, 0, len(in))
	for _, item := range in {
		out = append(out, LegendEntry{
			Descripcion:        item.Descripcion,
			DescripcionLeyenda: item.DescripcionLeyenda,
			IDTecno:            item.IDTecno,
			Nombre:             item.Nombre,
			Rango:              item.Rango,
		})
	}
	return out
}

func bboxFromStrings(west, south, east, north string) (BBox, error) {
	w, err := parseFloat(west)
	if err != nil {
		return BBox{}, err
	}
	s, err := parseFloat(south)
	if err != nil {
		return BBox{}, err
	}
	e, err := parseFloat(east)
	if err != nil {
		return BBox{}, err
	}
	n, err := parseFloat(north)
	if err != nil {
		return BBox{}, err
	}
	return BBox{West: w, South: s, East: e, North: n}, nil
}

func (b BBox) Intersects(other BBox) bool {
	return b.West <= other.East && b.East >= other.West && b.South <= other.North && b.North >= other.South
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func resolveBaseDir(baseDir string) string {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		if env := strings.TrimSpace(os.Getenv("COVERAGE_DATA_DIR")); env != "" {
			baseDir = env
		} else {
			if found, ok := discoverBaseDir(); ok {
				baseDir = found
			} else {
				baseDir = "data"
			}
		}
	}
	return baseDir
}

func discoverBaseDir() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "data")
		if _, err := os.Stat(filepath.Join(candidate, "movistar_cobertura", "manifest.json")); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// ---- Manifests internos ----

type movistarManifest struct {
	GeneratedAt string `json:"generated_at"`
	Source      struct {
		ApiBase    string `json:"api_base"`
		EmbedPage  string `json:"embed_page"`
		PublicPage string `json:"public_page"`
	} `json:"source"`
	Technologies []struct {
		ActiveLocalityCities int                   `json:"active_locality_cities"`
		CallKMLURLs          []string              `json:"call_kml_urls"`
		Cities               int                   `json:"cities"`
		Departments          int                   `json:"departments"`
		ID                   string                `json:"id"`
		KMLManifestPath      string                `json:"kml_manifest_path"`
		Legend               []movistarLegendEntry `json:"legend"`
		Localities           int                   `json:"localities"`
		Name                 string                `json:"name"`
	} `json:"technologies"`
}

type movistarLegendEntry struct {
	Descripcion        string `json:"descripcion"`
	DescripcionLeyenda string `json:"descripcion_leyenda"`
	IDTecno            string `json:"id_tecno"`
	Nombre             string `json:"nombre"`
	Rango              string `json:"rango"`
}

type movistarKMLManifest struct {
	Roots []struct {
		GroundOverlays []struct {
			BBox struct {
				East  string `json:"east"`
				North string `json:"north"`
				South string `json:"south"`
				West  string `json:"west"`
			} `json:"bbox"`
			URL string `json:"url"`
		} `json:"ground_overlays"`
	} `json:"roots"`
}

type claroManifest struct {
	BaseURLs    map[string]string `json:"base_urls"`
	GeneratedAt string            `json:"generated_at"`
	Localities  []struct {
		DepartmentID     string `json:"department_id"`
		DepartmentName   string `json:"department_name"`
		LocalityID       string `json:"locality_id"`
		LocalityName     string `json:"locality_name"`
		MunicipalityID   string `json:"municipality_id"`
		MunicipalityName string `json:"municipality_name"`
	} `json:"localities"`
	Source struct {
		MapPage    string `json:"map_page"`
		PublicPage string `json:"public_page"`
	} `json:"source"`
}

type tigoManifest struct {
	Admins []struct {
		AdminID      string `json:"admin_id"`
		AdminName    string `json:"admin_name"`
		CityID       string `json:"city_id"`
		DepartmentID string `json:"department_id"`
		Lat          string `json:"lat"`
		Lng          string `json:"lng"`
	} `json:"admins"`
	Cities []struct {
		CityID       string `json:"city_id"`
		CityName     string `json:"city_name"`
		DepartmentID string `json:"department_id"`
	} `json:"cities"`
	Departments []struct {
		DepartmentID   string `json:"department_id"`
		DepartmentName string `json:"department_name"`
	} `json:"departments"`
	GeneratedAt string   `json:"generated_at"`
	Notes       []string `json:"notes"`
	PageUpdate  string   `json:"page_update"`
	Source      struct {
		AdminsURL      string `json:"admins_url"`
		CitiesURL      string `json:"cities_url"`
		DateURL        string `json:"date_url"`
		DepartmentsURL string `json:"departments_url"`
		MapPage        string `json:"map_page"`
		PublicPage     string `json:"public_page"`
		ScriptURL      string `json:"script_url"`
	} `json:"source"`
	Technologies []struct {
		ID               string   `json:"id"`
		TileURLTemplates []string `json:"tile_url_templates"`
	} `json:"technologies"`
}

type womManifest struct {
	Layers []struct {
		Abstract string `json:"abstract"`
		BBox     *struct {
			Lower string `json:"lower"`
			Upper string `json:"upper"`
		} `json:"bbox"`
		Formats      []string `json:"formats"`
		Identifier   string   `json:"identifier"`
		ResourceURLs []struct {
			Format       string `json:"format"`
			ResourceType string `json:"resource_type"`
			Template     string `json:"template"`
		} `json:"resource_urls"`
		Styles []struct {
			Identifier string `json:"identifier"`
			IsDefault  bool   `json:"is_default"`
		} `json:"styles"`
		TileMatrixSets []string `json:"tile_matrix_sets"`
		Title          string   `json:"title"`
	} `json:"layers"`
	Notes      []string `json:"notes"`
	PageUpdate string   `json:"page_update"`
	Source     struct {
		CSVURL     string `json:"csv_url"`
		PublicPage string `json:"public_page"`
		WMTSURL    string `json:"wmts_url"`
	} `json:"source"`
	Summary struct {
		CoverageLayers int `json:"coverage_layers"`
		HelperLayers   int `json:"helper_layers"`
		TileMatrixSets int `json:"tile_matrix_sets"`
	} `json:"summary"`
	WMTS struct {
		CapabilitiesPath string `json:"capabilities_path"`
		CapabilitiesURL  string `json:"capabilities_url"`
		LayersPath       string `json:"layers_path"`
		URL              string `json:"url"`
	} `json:"wmts"`
}
