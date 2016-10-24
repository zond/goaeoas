package goaeoas

import (
	"io"

	"golang.org/x/net/html"
)

type Node struct {
	*html.Node
}

func NewEl(s string) *Node {
	return &Node{
		&html.Node{
			Type: html.ElementNode,
			Data: s,
		},
	}
}

func (n *Node) AddText(s string) *Node {
	textNode := &html.Node{
		Type: html.TextNode,
		Data: s,
	}
	n.Node.AppendChild(textNode)
	return &Node{textNode}
}

func (n *Node) AddNode(node *Node) *Node {
	n.Node.AppendChild(node.Node)
	return n
}

func (n *Node) Render(w io.Writer) error {
	return html.Render(w, n.Node)
}

func (n *Node) AddEl(s string, attrs ...string) *Node {
	elNode := &html.Node{
		Type: html.ElementNode,
		Data: s,
	}
	for i := 0; i < len(attrs); i += 2 {
		elNode.Attr = append(elNode.Attr, html.Attribute{
			Key: attrs[i],
			Val: attrs[i+1],
		})
	}
	n.Node.AppendChild(elNode)
	return &Node{elNode}
}
