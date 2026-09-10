// Vendored from https://github.com/highlightjs/highlightjs-gdscript/blob/4b584e9c8853348d58eb23ad3e867b015a6a89d7/src/languages/gdscript.js
// See gdscript.LICENSE. Local change: ES module export for GDScript only.
/*
Language: GDScript
Author: Khairul Hidayat <me@khairul.my.id>, Nelson Sylvest*r Fritsch <info@nelsonfritsch.de>, Hugo Locurcio <hugo.locurcio@hugo.pro>
Description: Programming language for Godot Engine
*/

function GDScript(hljs) {
	var KEYWORDS = {
		keyword:
			"and in not or self void as assert breakpoint class class_name " +
			"extends is func setget signal tool yield const enum export " +
			"onready static var break continue if elif else for pass return " +
			"match while remote sync master puppet remotesync mastersync " +
			"puppetsync",

		built_in:
			"Color8 ColorN abs acos asin atan atan2 bytes2var " +
			"cartesian2polar ceil char clamp convert cos cosh db2linear " +
			"decimals dectime deg2rad dict2inst ease exp floor fmod fposmod " +
			"funcref get_stack hash inst2dict instance_from_id inverse_lerp " +
			"is_equal_approx is_inf is_instance_valid is_nan is_zero_approx " +
			"len lerp lerp_angle linear2db load log max min move_toward " +
			"nearest_po2 ord parse_json polar2cartesian posmod pow preload " +
			"print_stack push_error push_warning rad2deg rand_range " +
			"rand_seed randf randi randomize range_lerp round seed sign sin " +
			"sinh smoothstep sqrt step_decimals stepify str str2var tan tanh " +
			"to_json type_exists typeof validate_json var2bytes var2str " +
			"weakref wrapf wrapi bool int float String NodePath " +
			"Vector2 Rect2 Transform2D Vector3 Rect3 Plane " +
			"Quat Basis Transform Color RID Object NodePath " +
			"Dictionary Array PoolByteArray PoolIntArray " +
			"PoolRealArray PoolStringArray PoolVector2Array " +
			"PoolVector3Array PoolColorArray",

		literal: "true false null"
	};

	return {
		aliases: ["godot", "gdscript"],
		keywords: KEYWORDS,
		contains: [
			hljs.NUMBER_MODE,
			hljs.HASH_COMMENT_MODE,
			{
				className: "comment",
				begin: /"""/,
				end: /"""/
			},
			hljs.QUOTE_STRING_MODE,
			{
				variants: [
					{
						className: "function",
						beginKeywords: "func"
					},
					{
						className: "class",
						beginKeywords: "class"
					}
				],
				end: /:/,
				contains: [hljs.UNDERSCORE_TITLE_MODE]
			}
		]
	};
}

export default GDScript;
