// Copyright ©2018 The Gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mat

import (
	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas64"
)

var (
	triBand TriBanded
	_       Banded     = triBand
	_       Triangular = triBand

	triBandDense *TriBandDense
	_            Matrix           = triBandDense
	_            Triangular       = triBandDense
	_            Banded           = triBandDense
	_            TriBanded        = triBandDense
	_            RawTriBander     = triBandDense
	_            MutableTriBanded = triBandDense
)

// TriBanded is a triangular band matrix interface type.
type TriBanded interface {
	Banded

	// Triangle returns the number of rows/columns in the matrix and its
	// orientation.
	Triangle() (n int, kind TriKind)

	// TTri is the equivalent of the T() method in the Matrix interface but
	// guarantees the transpose is of triangular type.
	TTri() Triangular

	// TriBand returns the number of rows/columns in the matrix, the
	// size of the bandwidth, and the orientation.
	TriBand() (n, k int, kind TriKind)

	// TTriBand is the equivalent of the T() method in the Matrix interface but
	// guarantees the transpose is of banded triangular type.
	TTriBand() TriBanded
}

// A RawTriBander can return a blas64.TriangularBand representation of the receiver.
// Changes to the blas64.TriangularBand.Data slice will be reflected in the original
// matrix, changes to the N, K, Stride, Uplo and Diag fields will not.
type RawTriBander interface {
	RawTriBand() blas64.TriangularBand
}

// MutableTriBanded is a triangular band matrix interface type that allows
// elements to be altered.
type MutableTriBanded interface {
	TriBanded
	SetTriBand(i, j int, v float64)
}

var (
	tTriBand TransposeTriBand
	_        Matrix               = tTriBand
	_        TriBanded            = tTriBand
	_        Untransposer         = tTriBand
	_        UntransposeTrier     = tTriBand
	_        UntransposeBander    = tTriBand
	_        UntransposeTriBander = tTriBand
)

// TransposeTriBand is a type for performing an implicit transpose of a TriBanded
// matrix. It implements the TriBanded interface, returning values from the
// transpose of the matrix within.
type TransposeTriBand struct {
	TriBanded TriBanded
}

// At returns the value of the element at row i and column j of the transposed
// matrix, that is, row j and column i of the TriBanded field.
func (t TransposeTriBand) At(i, j int) float64 {
	return t.TriBanded.At(j, i)
}

// Dims returns the dimensions of the transposed matrix. TriBanded matrices are
// square and thus this is the same size as the original TriBanded.
func (t TransposeTriBand) Dims() (r, c int) {
	c, r = t.TriBanded.Dims()
	return r, c
}

// T performs an implicit transpose by returning the TriBand field.
func (t TransposeTriBand) T() Matrix {
	return t.TriBanded
}

// Triangle returns the number of rows/columns in the matrix and its orientation.
func (t TransposeTriBand) Triangle() (int, TriKind) {
	n, upper := t.TriBanded.Triangle()
	return n, !upper
}

// TTri performs an implicit transpose by returning the TriBand field.
func (t TransposeTriBand) TTri() Triangular {
	return t.TriBanded
}

// Bandwidth returns the upper and lower bandwidths of the matrix.
func (t TransposeTriBand) Bandwidth() (kl, ku int) {
	kl, ku = t.TriBanded.Bandwidth()
	return ku, kl
}

// TBand performs an implicit transpose by returning the TriBand field.
func (t TransposeTriBand) TBand() Banded {
	return t.TriBanded
}

// TriBand returns the number of rows/columns in the matrix, the
// size of the bandwidth, and the orientation.
func (t TransposeTriBand) TriBand() (n, k int, kind TriKind) {
	n, k, kind = t.TriBanded.TriBand()
	return n, k, !kind
}

// TTriBand performs an implicit transpose by returning the TriBand field.
func (t TransposeTriBand) TTriBand() TriBanded {
	return t.TriBanded
}

// Untranspose returns the Triangular field.
func (t TransposeTriBand) Untranspose() Matrix {
	return t.TriBanded
}

// UntransposeTri returns the underlying Triangular matrix.
func (t TransposeTriBand) UntransposeTri() Triangular {
	return t.TriBanded
}

// UntransposeBand returns the underlying Banded matrix.
func (t TransposeTriBand) UntransposeBand() Banded {
	return t.TriBanded
}

// UntransposeTriBand returns the underlying TriBanded matrix.
func (t TransposeTriBand) UntransposeTriBand() TriBanded {
	return t.TriBanded
}

// TriBandDense represents a triangular band matrix in dense storage format.
type TriBandDense struct {
	mat blas64.TriangularBand
}

// NewTriBandDense creates a new triangular banded matrix with n rows and columns,
// k bands in the direction of the specified kind. If data == nil,
// a new slice is allocated for the backing slice. If len(data) == n*(k+1),
// data is used as the backing slice, and changes to the elements of the returned
// TriBandDense will be reflected in data. If neither of these is true, NewTriBandDense
// will panic. k must be at least zero and less than n, otherwise NewTriBandDense will panic.
//
// The data must be arranged in row-major order constructed by removing the zeros
// from the rows outside the band and aligning the diagonals. For example, if
// the upper-triangular banded matrix
//    1  2  3  0  0  0
//    0  4  5  6  0  0
//    0  0  7  8  9  0
//    0  0  0 10 11 12
//    0  0  0 0  13 14
//    0  0  0 0  0  15
// becomes (* entries are never accessed)
//     1  2  3
//     4  5  6
//     7  8  9
//    10 11 12
//    13 14  *
//    15  *  *
// which is passed to NewTriBandDense as []float64{1, 2, ..., 15, *, *, *}
// with k=2 and kind = mat.Upper.
// The lower triangular banded matrix
//    1  0  0  0  0  0
//    2  3  0  0  0  0
//    4  5  6  0  0  0
//    0  7  8  9  0  0
//    0  0 10 11 12  0
//    0  0  0 13 14 15
// becomes (* entries are never accessed)
//     *  *  1
//     *  2  3
//     4  5  6
//     7  8  9
//    10 11 12
//    13 14 15
// which is passed to NewTriBandDense as []float64{*, *, *, 1, 2, ..., 15}
// with k=2 and kind = mat.Lower.
// Only the values in the band portion of the matrix are used.
func NewTriBandDense(n, k int, kind TriKind, data []float64) *TriBandDense {
	if n <= 0 || k < 0 {
		if n == 0 {
			panic(ErrZeroLength)
		}
		panic("mat: negative dimension")
	}
	if k+1 > n {
		panic("mat: band out of range")
	}
	bc := k + 1
	if data != nil && len(data) != n*bc {
		panic(ErrShape)
	}
	if data == nil {
		data = make([]float64, n*bc)
	}
	uplo := blas.Lower
	if kind {
		uplo = blas.Upper
	}
	return &TriBandDense{
		mat: blas64.TriangularBand{
			Uplo:   uplo,
			Diag:   blas.NonUnit,
			N:      n,
			K:      k,
			Data:   data,
			Stride: bc,
		},
	}
}

// Dims returns the number of rows and columns in the matrix.
func (t *TriBandDense) Dims() (r, c int) {
	return t.mat.N, t.mat.N
}

// T performs an implicit transpose by returning the receiver inside a Transpose.
func (t *TriBandDense) T() Matrix {
	return Transpose{t}
}

// IsZero returns whether the receiver is zero-sized. Zero-sized matrices can be the
// receiver for size-restricted operations. TriBandDense matrices can be zeroed using Reset.
func (t *TriBandDense) IsZero() bool {
	// It must be the case that t.Dims() returns
	// zeros in this case. See comment in Reset().
	return t.mat.Stride == 0
}

// Reset zeros the dimensions of the matrix so that it can be reused as the
// receiver of a dimensionally restricted operation.
//
// See the Reseter interface for more information.
func (t *TriBandDense) Reset() {
	t.mat.N = 0
	t.mat.Stride = 0
	t.mat.K = 0
	t.mat.Data = t.mat.Data[:0]
}

// Zero sets all of the matrix elements to zero.
func (t *TriBandDense) Zero() {
	if t.isUpper() {
		for i := 0; i < t.mat.N; i++ {
			u := min(1+t.mat.K, t.mat.N-i)
			zero(t.mat.Data[i*t.mat.Stride : i*t.mat.Stride+u])
		}
		return
	}
	for i := 0; i < t.mat.N; i++ {
		l := max(0, t.mat.K-i)
		zero(t.mat.Data[i*t.mat.Stride+l : i*t.mat.Stride+t.mat.K+1])
	}
}

func (t *TriBandDense) isUpper() bool {
	return isUpperUplo(t.mat.Uplo)
}

func (t *TriBandDense) triKind() TriKind {
	return TriKind(isUpperUplo(t.mat.Uplo))
}

// Triangle returns the dimension of t and its orientation. The returned
// orientation is only valid when n is not zero.
func (t *TriBandDense) Triangle() (n int, kind TriKind) {
	return t.mat.N, t.triKind()
}

// TTri performs an implicit transpose by returning the receiver inside a TransposeTri.
func (t *TriBandDense) TTri() Triangular {
	return TransposeTri{t}
}

// Bandwidth returns the upper and lower bandwidths of the matrix.
func (t *TriBandDense) Bandwidth() (kl, ku int) {
	if t.isUpper() {
		return 0, t.mat.K
	}
	return t.mat.K, 0
}

// TBand performs an implicit transpose by returning the receiver inside a TransposeBand.
func (t *TriBandDense) TBand() Banded {
	return TransposeBand{t}
}

// TriBand returns the number of rows/columns in the matrix, the
// size of the bandwidth, and the orientation.
func (t *TriBandDense) TriBand() (n, k int, kind TriKind) {
	return t.mat.N, t.mat.K, TriKind(!t.IsZero()) && t.triKind()
}

// TTriBand performs an implicit transpose by returning the receiver inside a TransposeTriBand.
func (t *TriBandDense) TTriBand() TriBanded {
	return TransposeTriBand{t}
}

// RawTriBand returns the underlying blas64.TriangularBand used by the receiver.
// Changes to the blas64.TriangularBand.Data slice will be reflected in the original
// matrix, changes to the N, K, Stride, Uplo and Diag fields will not.
func (t *TriBandDense) RawTriBand() blas64.TriangularBand {
	return t.mat
}

// SetRawTriBand sets the underlying blas64.TriangularBand used by the receiver.
// Changes to elements in the receiver following the call will be reflected
// in the input.
//
// The supplied TriangularBand must not use blas.Unit storage format.
func (t *TriBandDense) SetRawTriBand(mat blas64.TriangularBand) {
	if mat.Diag == blas.Unit {
		panic("mat: cannot set TriBand with Unit storage")
	}
	t.mat = mat
}

// DiagView returns the diagonal as a matrix backed by the original data.
func (t *TriBandDense) DiagView() Diagonal {
	if t.mat.Diag == blas.Unit {
		panic("mat: cannot take view of Unit diagonal")
	}
	n := t.mat.N
	data := t.mat.Data
	if !t.isUpper() {
		data = data[t.mat.K:]
	}
	return &DiagDense{
		mat: blas64.Vector{
			N:    n,
			Inc:  t.mat.Stride,
			Data: data[:(n-1)*t.mat.Stride+1],
		},
	}
}

// Trace returns the trace.
func (t *TriBandDense) Trace() float64 {
	rb := t.RawTriBand()
	var tr float64
	var offsetIndex int
	if rb.Uplo == blas.Lower {
		offsetIndex = rb.K
	}
	for i := 0; i < rb.N; i++ {
		tr += rb.Data[offsetIndex+i*rb.Stride]
	}
	return tr
}
