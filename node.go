package goaeoas

import (
	"fmt"
	"io"

	"golang.org/x/net/html"
)

type Node struct {
	*html.Node
}

func NewEl(s string, attrs ...string) *Node {
	elNode := &Node{
		&html.Node{
			Type: html.ElementNode,
			Data: s,
		},
	}
	for i := 0; i < len(attrs); i += 2 {
		elNode.Attr = append(elNode.Attr, html.Attribute{
			Key: attrs[i],
			Val: attrs[i+1],
		})
	}
	return elNode
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

func (n *Node) String() string {
	return fmt.Sprintf("<%s %+v>", n.Node.Data, n.Node.Attr)
}

func (n *Node) AddEl(s string, attrs ...string) *Node {
	elNode := NewEl(s, attrs...)
	n.Node.AppendChild(elNode.Node)
	return elNode
}
