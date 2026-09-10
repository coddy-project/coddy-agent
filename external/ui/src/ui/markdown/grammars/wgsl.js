// Vendored from https://github.com/highlightjs/highlightjs-wgsl/blob/418185581f59e67c257a1d2fa43ec3f9103382df/src/languages/wgsl.js
// See wgsl.LICENSE. Local change: ES module export for GDScript only.
// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
Language: WGSL
Description: WebGPU Shading Language
Author: Arman Uguray <arman.uguray@gmail.com>
Website: https://www.w3.org/TR/WGSL
Category: graphics
*/

export default function(hljs) {
  return {
    name: "WGSL",
    keywords: {
      keyword:
        // Statements
        'break continue continuing discard else for if loop return while switch case default '

        // Declarations/directives
        + 'alias const const_assert diagnostic enable fn let override requires struct var '

        // Storage classes
        + 'function private workgroup uniform storage '

        // Access modes
        + 'read read_write write '

        // Interpolation
        + 'center centroid flat linear perspective sample '

        // Built-in value enumerants
        + 'frag_depth front_facing global_invocation_id instance_index local_invocation_id '
        + 'local_invocation_index num_workgroups position sample_index sample_mask '
        + 'vertex_index workgroup_id '

        // Texel formats
        + 'rgba8unorm rgba8snorm rgba8uint rgba8sint rgba16uint rgba16sint rgba16float '
        + 'r32uint r32sint r32float rg32uint rg32sint rg32float rgba32uint rgba32sint '
        + 'rgba32float bgra8unorm '

        // Attributes
        + 'align binding builtin compute const diagnostic fragment group id interpolate '
        + 'invariant location must_use size vertex workgroup_size',

      type:
        // Scalars
        'bool f32 f16 i32 i16 u32 u16 '

        // Vector
        + 'vec2 vec2f vec2i vec2u vec3 vec3f vec3i vec3u vec4 vec4f vec4i vec4u '

        // Matrix
        + 'mat2x2 mat2x2f mat2x2i mat2x2u '
        + 'mat2x3 mat2x3f mat2x3i mat2x3u '
        + 'mat2x4 mat2x4f mat2x4i mat2x4u '
        + 'mat3x2 mat3x2f mat3x2i mat3x2u '
        + 'mat3x3 mat3x3f mat3x3i mat3x3u '
        + 'mat3x4 mat3x4f mat3x4i mat3x4u '
        + 'mat4x2 mat4x2f mat4x2i mat4x2u '
        + 'mat4x3 mat4x3f mat4x3i mat4x3u '
        + 'mat4x4 mat4x4f mat4x4i mat4x4u '

        // Texture
        + 'texture_1d texture_2d texture_2d_array '
        + 'texture_3d texture_cube texture_cube_array '
        + 'texture_multisampled_2d texture_storage_3d '
        + 'texture_storage_1d texture_storage_2d texture_storage_2d_array '
        + 'texture_depth_2d texture_depth_2d_array texture_depth_cube texture_depth_cube_array '
        + 'sampler sampler_comparison '

        // Other
        + 'array atomic ptr',

      built_in:
        // Bit reinterpretation
        'bitcast '

        // Logical
        + 'all any select '

        // Array
        + 'arrayLength '

        // Numeric
        + 'abs acos acosh asin asinh atan atanh atan2 ceil clamp cos cosh countLeadingZeros '
        + 'countOneBits countTrailingZeros cross degrees determinant distance dot dot4U8Packed '
        + 'dot4I8Packed exp exp2 extractBits faceForward firstLeadingBit firstTrailingBit floor '
        + 'fma fract frexp inverseBits inverseSqrt ldexp length log log2 max min mix modf '
        + 'normalize pow quantizeToF16 radians reflect refract reverseBits round saturate '
        + 'sign sin sinh smoothstep sqrt step tan tanh transpose trunc '

        // Derivative
        + 'dpdx dpdxCoarse dpdxFine dpdy dpdyCoarse dpdyFine fwidth fwidthCoarse fwidthFine '

        // Texture
        + 'textureDimensions textureGather textureGatherCompare textureLoad textureNumLayers '
        + 'textureNumLevels textureNumSamples textureSample '
        + 'textureSampleBias textureSampleCompare textureSampleCompareLevel textureSampleGrad '
        + 'textureSampleLevel textureSampleBaseClampToEdge textureStore '

        // Atomic
        + 'atomicLoad atomicStore atomicAdd atomicSub atomicMax atomicMin atomicAnd atomicOr '
        + 'atomicXor atomicExchange atomicCompareExchangeWeak '

        // Data (un)packing
        + 'pack4x8snorm pack4x8unorm pack4xI8 pack4xU8 pack4xI8Clamp pack4xU8Clamp '
        + 'pack2x16snorm pack2x16unorm pack2x16float '
        + 'unpack4x8snorm unpack4x8unorm unpack4xI8 unpack4xU8 unpack2x16snorm '
        + 'unpack2x16unorm unpack2x16float '

        // Synchronization
        + 'storageBarrier textureBarrier workgroupBarrier workgroupUniformLoad',

      literal: 'true false'
    },
    illegal: '"',
    contains: [
      hljs.C_LINE_COMMENT_MODE,
      hljs.C_BLOCK_COMMENT_MODE,
      hljs.C_NUMBER_MODE,
    ]
  }
}
