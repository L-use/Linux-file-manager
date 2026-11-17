package main

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"net/http"
	"path"
	"strings"
)

const pptxLimit = 48 << 20

type presentationRelationship struct {
	ID     string `xml:"Id,attr"`
	Target string `xml:"Target,attr"`
	Mode   string `xml:"TargetMode,attr"`
}

type slideTransform struct {
	Offset struct {
		X int64 `xml:"x,attr"`
		Y int64 `xml:"y,attr"`
	} `xml:"off"`
	Extent struct {
		X int64 `xml:"cx,attr"`
		Y int64 `xml:"cy,attr"`
	} `xml:"ext"`
}

type slideItem struct {
	Text   []string `json:"text,omitempty"`
	Image  string   `json:"image,omitempty"`
	X      float64  `json:"x"`
	Y      float64  `json:"y"`
	Width  float64  `json:"width"`
	Height float64  `json:"height"`
}

type presentationPreview struct {
	Page  int         `json:"page"`
	Count int         `json:"count"`
	Ratio float64     `json:"ratio"`
	Items []slideItem `json:"items"`
}

func presentationMembers(f io.ReaderAt, size int64) (map[string]*zip.File, error) {
	if size > pptxLimit {
		return nil, failure(413, "docLimit")
	}
	z, err := zip.NewReader(f, size)
	if err != nil {
		return nil, failure(415, "presentationError")
	}
	if len(z.File) > 12000 {
		return nil, failure(413, "docLimit")
	}
	members := make(map[string]*zip.File)
	for _, f := range z.File {
		members[f.Name] = f
	}
	return members, nil
}

func presentationXML(members map[string]*zip.File, name string, dst any) error {
	member := members[name]
	if member == nil {
		return failure(415, "presentationError")
	}
	if member.UncompressedSize64 > 8<<20 {
		return failure(413, "docLimit")
	}
	r, err := member.Open()
	if err != nil {
		return failure(415, "presentationError")
	}
	defer r.Close()
	if err := xml.NewDecoder(io.LimitReader(r, 8<<20)).Decode(dst); err != nil {
		return failure(415, "presentationError")
	}
	return nil
}

func presentationLinks(members map[string]*zip.File, part string) map[string]string {
	base := path.Dir(part)
	var relationships struct {
		Links []presentationRelationship `xml:"Relationship"`
	}
	_ = presentationXML(members, path.Join(base, "_rels", path.Base(part)+".rels"), &relationships)
	links := make(map[string]string)
	for _, link := range relationships.Links {
		if link.Mode != "" && link.Mode != "Internal" {
			continue
		}
		if strings.ContainsAny(link.Target, ":%\\") {
			continue
		}
		if strings.HasPrefix(link.Target, "/") {
			links[link.ID] = path.Clean(strings.TrimPrefix(link.Target, "/"))
		} else {
			links[link.ID] = path.Join(base, link.Target)
		}
	}
	return links
}

func (s *fileStore) previewPresentation(name string, number int) (presentationPreview, error) {
	out := presentationPreview{Page: number, Items: make([]slideItem, 0)}
	if !strings.EqualFold(path.Ext(name), ".pptx") {
		return out, failure(415, "unsupported")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return out, err
	}
	defer f.Close()
	members, err := presentationMembers(f, info.Size())
	if err != nil {
		return out, err
	}
	var presentation struct {
		Slides []struct {
			Attr []xml.Attr `xml:",any,attr"`
		} `xml:"sldIdLst>sldId"`
		Size struct {
			X int64 `xml:"cx,attr"`
			Y int64 `xml:"cy,attr"`
		} `xml:"sldSz"`
	}
	const main = "ppt/presentation.xml"
	if err := presentationXML(members, main, &presentation); err != nil {
		return out, err
	}
	out.Count = len(presentation.Slides)
	if number < 1 || number > out.Count {
		return out, failure(404, "missing")
	}
	links := presentationLinks(members, main)
	var slide string
	for _, attr := range presentation.Slides[number-1].Attr {
		if attr.Name.Local == "id" && attr.Name.Space != "" {
			slide = links[attr.Value]
			break
		}
	}
	if !strings.HasPrefix(slide, "ppt/slides/") {
		return out, failure(415, "presentationError")
	}
	width, height := presentation.Size.X, presentation.Size.Y
	if width < 1 || height < 1 {
		width, height = 12192000, 6858000
	}
	out.Ratio = float64(width) / float64(height)
	var data struct {
		Tree struct {
			Shapes []struct {
				XMLName   xml.Name
				Transform slideTransform `xml:"spPr>xfrm"`
				Blip      struct {
					Embed string `xml:"embed,attr"`
				} `xml:"blipFill>blip"`
				Paragraphs []struct {
					Runs []struct {
						Text string `xml:"t"`
					} `xml:"r"`
				} `xml:"txBody>p"`
			} `xml:",any"`
		} `xml:"cSld>spTree"`
	}
	if err := presentationXML(members, slide, &data); err != nil {
		return out, err
	}
	images := presentationLinks(members, slide)
	for _, shape := range data.Tree.Shapes {
		box := shape.Transform
		item := slideItem{X: float64(box.Offset.X) / float64(width), Y: float64(box.Offset.Y) / float64(height), Width: float64(box.Extent.X) / float64(width), Height: float64(box.Extent.Y) / float64(height)}
		for _, paragraph := range shape.Paragraphs {
			var line strings.Builder
			for _, run := range paragraph.Runs {
				line.WriteString(run.Text)
			}
			if line.Len() > 0 {
				item.Text = append(item.Text, line.String())
			}
		}
		if image := images[shape.Blip.Embed]; allowedSlideImage(image) {
			item.Image = image
		}
		if len(item.Text) == 0 && item.Image == "" {
			continue
		}
		if item.Width <= 0 || item.Height <= 0 {
			item.X, item.Y, item.Width, item.Height = .08, .08+float64(len(out.Items))*.13, .84, .13
		}
		out.Items = append(out.Items, item)
		if len(out.Items) >= 250 {
			break
		}
	}
	return out, nil
}

func allowedSlideImage(member string) bool {
	if !strings.HasPrefix(member, "ppt/media/") || path.Clean(member) != member {
		return false
	}
	switch strings.ToLower(path.Ext(member)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

func (s *fileStore) servePresentationImage(w http.ResponseWriter, name, part string) error {
	if !strings.EqualFold(path.Ext(name), ".pptx") || !allowedSlideImage(part) {
		return failure(400, "badRequest")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	members, err := presentationMembers(f, info.Size())
	if err != nil {
		return err
	}
	member := members[part]
	if member == nil {
		return failure(404, "missing")
	}
	if member.UncompressedSize64 > 12<<20 {
		return failure(413, "docLimit")
	}
	input, err := member.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	w.Header().Set("Content-Type", mediaType(strings.ToLower(path.Ext(part))))
	_, _ = io.Copy(w, io.LimitReader(input, int64(member.UncompressedSize64)))
	return nil
}
