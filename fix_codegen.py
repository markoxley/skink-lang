import sys

path = 'c:\\Development\\skink\\skink-lang\\compiler\\codegen\\codegen.go'
with open(path, 'r', encoding='utf-8') as f:
    content = f.read()

# Replacement 1: len() builtin condition
old1 = '\t\t\t\tif meta, ok := cg.arraySizes[cg.currentFnName+":"+allocaKey]; ok {\n\t\t\t\t\treturn fmt.Sprintf("%d", meta.len)\n\t\t\t\t}'
new1 = '\t\t\t\tif meta, ok := cg.arraySizes[cg.currentFnName+":"+allocaKey]; ok && !meta.heapAlloc {\n\t\t\t\t\treturn fmt.Sprintf("%d", meta.len)\n\t\t\t\t}'
content = content.replace(old1, new1)

# Replacement 2: runtime array length in len() identifier case
old2 = '\t\t\t\t// No compile-time info: read length prefix at runtime.\n\t\t\t\targReg := cg.emitExpression(arg)\n\t\t\t\targType := cg.exprLLType(arg)\n\t\t\t\tif argType == "i8*" {\n\t\t\t\t\t// String: use strlen.\n\t\t\t\t\tlenReg := cg.nextReg()\n\t\t\t\t\tcg.writef("  %s = call i64 @strlen(i8* %s)\\n", lenReg, argReg)\n\t\t\t\t\ttruncReg := cg.nextReg()\n\t\t\t\t\tcg.writef("  %s = trunc i64 %s to i32\\n", truncReg, lenReg)\n\t\t\t\t\treturn truncReg\n\t\t\t\t}\n\t\t\t\t// Array: read i64 length prefix at offset -8, but return 0 for null.\n\t\t\t\trawPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = bitcast %s %s to i8*\\n", rawPtr, argType, argReg)\n\t\t\t\tisNull := cg.nextReg()\n\t\t\t\tcg.writef("  %s = icmp eq i8* %s, null\\n", isNull, rawPtr)\n\t\t\t\tlenPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\\n", lenPtr, rawPtr)\n\t\t\t\ttypedLenPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = bitcast i8* %s to i64*\\n", typedLenPtr, lenPtr)\n\t\t\t\tloadedLen := cg.nextReg()\n\t\t\t\tcg.writef("  %s = load i64, i64* %s\\n", loadedLen, typedLenPtr)\n\t\t\t\tlenReg := cg.nextReg()\n\t\t\t\tcg.writef("  %s = select i1 %s, i64 0, i64 %s\\n", lenReg, isNull, loadedLen)\n\t\t\t\ttruncReg := cg.nextReg()\n\t\t\t\tcg.writef("  %s = trunc i64 %s to i32\\n", truncReg, lenReg)\n\t\t\t\treturn truncReg'
new2 = '\t\t\t\t// No compile-time info: compute string/array length at runtime.\n\t\t\t\targReg := cg.emitExpression(arg)\n\t\t\t\targType := cg.exprLLType(arg)\n\t\t\t\tif argType == "i8*" {\n\t\t\t\t\t// String: use strlen.\n\t\t\t\t\tlenReg := cg.nextReg()\n\t\t\t\t\tcg.writef("  %s = call i64 @strlen(i8* %s)\\n", lenReg, argReg)\n\t\t\t\t\ttruncReg := cg.nextReg()\n\t\t\t\t\tcg.writef("  %s = trunc i64 %s to i32\\n", truncReg, lenReg)\n\t\t\t\t\treturn truncReg\n\t\t\t\t}\n\t\t\t\treturn cg.emitRuntimeArrayLen(argReg, argType)'
content = content.replace(old2, new2)

# Replacement 3: fallback runtime length
old3 = '\t\t\tif strings.HasSuffix(argType, "*") {\n\t\t\t\trawPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = bitcast %s %s to i8*\\n", rawPtr, argType, argReg)\n\t\t\t\tisNull := cg.nextReg()\n\t\t\t\tcg.writef("  %s = icmp eq i8* %s, null\\n", isNull, rawPtr)\n\t\t\t\tlenPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = getelementptr inbounds i8, i8* %s, i64 -8\\n", lenPtr, rawPtr)\n\t\t\t\ttypedLenPtr := cg.nextReg()\n\t\t\t\tcg.writef("  %s = bitcast i8* %s to i64*\\n", typedLenPtr, lenPtr)\n\t\t\t\tloadedLen := cg.nextReg()\n\t\t\t\tcg.writef("  %s = load i64, i64* %s\\n", loadedLen, typedLenPtr)\n\t\t\t\tlenReg := cg.nextReg()\n\t\t\t\tcg.writef("  %s = select i1 %s, i64 0, i64 %s\\n", lenReg, isNull, loadedLen)\n\t\t\t\ttruncReg := cg.nextReg()\n\t\t\t\tcg.writef("  %s = trunc i64 %s to i32\\n", truncReg, lenReg)\n\t\t\t\treturn truncReg\n\t\t\t}'
new3 = '\t\t\tif strings.HasSuffix(argType, "*") {\n\t\t\t\treturn cg.emitRuntimeArrayLen(argReg, argType)\n\t\t\t}'
content = content.replace(old3, new3)

with open(path, 'w', encoding='utf-8') as f:
    f.write(content)

print('done')
