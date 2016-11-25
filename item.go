package goaeoas

import (
	"encoding/json"
	"reflect"
	"sort"
)

type Item struct {
	Properties interface{}
	Name       string
	Desc       [][]string
	Links      Links
}

func NewItem(i interface{}) *Item {
	return &Item{
		Properties: i,
	}
}

func (i *Item) SetDesc(s [][]string) *Item {
	i.Desc = s
	return i
}

func (i *Item) SetName(s string) *Item {
	i.Name = s
	return i
}

func (i *Item) AddLink(l Link) *Item {
	i.Links = append(i.Links, l)
	return i
}

func (i Item) MarshalJSON() ([]byte, error) {
	propertyValue := reflect.ValueOf(i.Properties)
	for propertyValue.Kind() == reflect.Ptr {
		propertyValue = propertyValue.Elem()
	}
	return json.Marshal(struct {
		Name       string
		Properties interface{}
		Desc       [][]string `json:",omitempty"`
		Type       string
		Links      Links
	}{
		Properties: i.Properties,
		Desc:       i.Desc,
		Type:       propertyValue.Type().Name(),
		Links:      i.Links,
		Name:       i.Name,
	})
}

func (i Item) HTMLNode() (*Node, error) {
	selfLink := ""
	restLinks := Links{}
	for _, link := range i.Links {
		if link.Rel == "self" {
			u, err := link.Resolve()
			if err != nil {
				return nil, err
			}
			selfLink = u
		} else {
			restLinks = append(restLinks, link)
		}
	}
	sort.Sort(restLinks)

	itemNode := NewEl("section")
	titleNode := itemNode.AddEl("header")
	if selfLink == "" {
		titleNode.AddText(i.Name)
	} else {
		titleNode.AddEl("a", "href", selfLink).AddText(i.Name)
	}
	if len(i.Desc) > 0 {
		descNode := itemNode.AddEl("section")
		descNode.AddEl("header").AddText("Description")
		for _, part := range i.Desc {
			if len(part) > 0 {
				articleNode := descNode.AddEl("article")
				articleNode.AddEl("header").AddText(part[0])
				for _, paragraph := range part[1:] {
					articleNode.AddEl("p").AddText(paragraph)
				}
			}
		}
	}
	propNode := itemNode.AddEl("section")
	propNode.AddEl("header").AddText("Properties")
	if list, ok := i.Properties.(List); ok {
		listNode := propNode.AddEl("ul")
		for _, item := range list {
			itemNode, err := item.HTMLNode()
			if err != nil {
				return nil, err
			}
			listNode.AddEl("ul").AddNode(itemNode)
		}
	} else {
		preNode := propNode.AddEl("article").AddEl("pre")
		pretty, err := json.MarshalIndent(i.Properties, "  ", "  ")
		if err != nil {
			return nil, err
		}
		preNode.AddText(string(pretty))
	}
	if len(restLinks) > 0 {
		navNode := itemNode.AddEl("nav")
		for _, link := range restLinks {
			linkNode, err := link.HTMLNode()
			if err != nil {
				return nil, err
			}
			navNode.AddNode(linkNode)
		}
	}
	return itemNode, nil
}
