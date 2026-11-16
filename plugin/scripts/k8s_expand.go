// This file is a copy of Kubernetes' third_party/forked/golang/expansion/expand.go
// (kubernetes/kubernetes commit 7d2646453975fcb939cc1dd531c7126ac4004da4), the
// code kubelet uses to expand $(VAR) references in env values, command, args,
// exec probes and subPathExpr. Only the package name and the identifiers were
// changed, so they don't clash with other code in package main. It is under the
// BSD license in K8S_EXPANSION_LICENSE.

package main

import (
	"bytes"
	"unicode/utf8"
)

const (
	k8sOperator        = '$'
	k8sReferenceOpener = '('
	k8sReferenceCloser = ')'
)

// k8sSyntaxWrap returns the input string wrapped by the expansion syntax.
func k8sSyntaxWrap(input string) string {
	return string(k8sOperator) + string(k8sReferenceOpener) + input + string(k8sReferenceCloser)
}

// k8sMappingFuncFor returns a mapping function for use with k8sExpand that
// implements the expansion semantics defined in the expansion spec; it
// returns the input string wrapped in the expansion syntax if no mapping
// for the input is found.
func k8sMappingFuncFor(context ...map[string]string) func(string) string {
	return func(input string) string {
		for _, vars := range context {
			val, ok := vars[input]
			if ok {
				return val
			}
		}

		return k8sSyntaxWrap(input)
	}
}

// k8sExpand replaces variable references in the input string according to
// the expansion spec using the given mapping function to resolve the
// values of variables.
func k8sExpand(input string, mapping func(string) string) string {
	var buf bytes.Buffer
	checkpoint := 0
	for cursor := 0; cursor < len(input); cursor++ {
		if input[cursor] == k8sOperator && cursor+1 < len(input) {
			// Copy the portion of the input string since the last
			// checkpoint into the buffer
			buf.WriteString(input[checkpoint:cursor])

			// Attempt to read the variable name as defined by the
			// syntax from the input string
			read, isVar, advance := k8sTryReadVariableName(input[cursor+1:])

			if isVar {
				// We were able to read a variable name correctly;
				// apply the mapping to the variable name and copy the
				// bytes into the buffer
				buf.WriteString(mapping(read))
			} else {
				// Not a variable name; copy the read bytes into the buffer
				buf.WriteString(read)
			}

			// Advance the cursor in the input string to account for
			// bytes consumed to read the variable name expression
			cursor += advance

			// Advance the checkpoint in the input string
			checkpoint = cursor + 1
		}
	}

	// Return the buffer and any remaining unwritten bytes in the
	// input string.
	return buf.String() + input[checkpoint:]
}

// k8sTryReadVariableName attempts to read a variable name from the input
// string and returns the content read from the input, whether that content
// represents a variable name to perform mapping on, and the number of bytes
// consumed in the input string.
//
// The input string is assumed not to contain the initial k8sOperator.
func k8sTryReadVariableName(input string) (string, bool, int) {
	r, size := utf8.DecodeRuneInString(input)
	switch r {
	case k8sOperator:
		// Escaped k8sOperator; return it.
		return input[0:size], false, size
	case k8sReferenceOpener:
		// Scan to expression closer
		for i := 1; i < len(input); i++ {
			if input[i] == k8sReferenceCloser {
				return input[1:i], true, i + 1
			}
		}

		// Incomplete reference; return it.
		return string(k8sOperator) + string(k8sReferenceOpener), false, 1
	default:
		// Not the beginning of an expression, ie, an k8sOperator
		// that doesn't begin an expression.  Return the k8sOperator
		// and the first rune in the string.

		return string(k8sOperator) + string(r), false, size
	}
}
